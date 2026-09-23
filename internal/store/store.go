// Package store 负责把解析结果落到 SQLite，并提供检索接口。
//
// 只用标准库 database/sql + 纯 Go 驱动 modernc.org/sqlite：本机没有 gcc，
// 任何需要 cgo 的驱动（mattn/go-sqlite3）都编译不了，这里刻意避开。
//
// 数据按语言分域：item/npc/mob/wz_file 的主键都含 lang，同一 ID 在不同语言包下
// 各存一行，互不覆盖；图标与语言无关，不落库。
//
// 实体域（Kind）决定行落在哪张表：物品、NPC、怪物共用一套同构 schema，
// 但必须分表——三个域的 ID 空间整体重叠，同表就会互相覆盖名称。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sqx6781268/MapleWzMeta/internal/extract"

	_ "modernc.org/sqlite"
)

// LegacyLang 是旧库（无 lang 列）迁移时回填的语言标识。
// 现有数据实测来自中文导出包，因此固定为 zh-CN。
const LegacyLang = "zh-CN"

// NoCategoryName 是"分类为空"在统计里的显示名。
// 它不是真实分类，检索时会被翻译回 category = ”。
const NoCategoryName = "(未分类)"

// Kind 是实体域，决定行落在哪张表。取值与 extract.Kind 一致，
// 由 scan 从文件路径推导后传入；存储层自己不做路径判断，避免规则两处漂移。
type Kind string

const (
	KindItem    Kind = "item"
	KindNPC     Kind = "npc"
	KindMob     Kind = "mob"
	KindSkill   Kind = "skill"
	KindMorph   Kind = "morph"
	KindReactor Kind = "reactor"
)

// Kinds 是全部实体域，顺序即界面展示顺序。
var Kinds = []Kind{KindItem, KindNPC, KindMob, KindSkill, KindMorph, KindReactor}

// ParseKind 解析外部输入；空串按物品处理（保持既有调用方与老链接兼容）。
func ParseKind(s string) (Kind, bool) {
	switch Kind(s) {
	case "", KindItem:
		return KindItem, true
	case KindNPC, KindMob, KindSkill, KindMorph, KindReactor:
		return Kind(s), true
	}
	return KindItem, false
}

// table 返回该实体域的表名。未知域回落 item——表名要拼进 SQL，
// 这里必须是白名单映射，不能接受外部字符串。
func (k Kind) table() string {
	switch k {
	case KindNPC:
		return "npc"
	case KindMob:
		return "mob"
	case KindSkill:
		return "skill"
	case KindMorph:
		return "morph"
	case KindReactor:
		return "reactor"
	}
	return "item"
}

// idPadLen 是物品 ID 的入库定长位数（与 extract.IDLen 一致）。
// 用户手输的常是原始 7 位号段，前缀检索要能同时命中补零形式。
const idPadLen = 8

// DB 包装 sqlite 连接。
type DB struct {
	sqlDB *sql.DB
	Path  string
}

// FileRec 是一个导出文件的指纹与处理结果，用于增量判断。
type FileRec struct {
	Path      string    // 相对 WZ 根的路径，正斜杠
	Size      int64     // 字节数
	ModTime   time.Time // 源文件修改时间（导出器保留了原始时间戳）
	Status    string    // ok / failed
	Err       string
	Repaired  int
	Rows      int // 该文件贡献的名称/属性条目数
	ScannedAt time.Time
	// IDs 是该文件贡献的物品 ID，用于"文件变动后精确覆盖"。
	// 旧库这一列是空数组，此时重载只能覆盖解析到的字段，无法反查并清除历史贡献。
	IDs []string
	// Kind 是该文件所属实体域，决定 IDs 写进溯源时的前缀。
	Kind Kind
}

// ItemRow 是关联后的实体行（物品 / NPC / 怪物同构，按 Kind 区分）。
// 字段标签与 /api 返回一致，便于 CLI 导出。
type ItemRow struct {
	Kind     Kind              `json:"kind,omitempty"`
	Lang     string            `json:"lang"`
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Descr    string            `json:"descr"`
	Category string            `json:"category"`
	Info     map[string]string `json:"info"`
	HasName  bool              `json:"hasName"`
	HasInfo  bool              `json:"hasInfo"`
	Edited   bool              `json:"edited"` // 是否在管理页手工改过（重载时默认跳过）
	Updated  time.Time         `json:"updatedAt,omitzero"`
}

// entityTable 是三张同构实体表（item / npc / mob）的 DDL 模板。
// 三个域共用"名称侧 + 属性侧合并成一行"的形状，但必须分表：
// 实测怪物/NPC 的 ID 补零后与物品 ID 整体重叠，同表就是 4,347 条名称污染。
const entityTable = `
CREATE TABLE IF NOT EXISTS %s (
  lang       TEXT NOT NULL,
  id         TEXT NOT NULL,
  name       TEXT NOT NULL DEFAULT '',
  descr      TEXT NOT NULL DEFAULT '',
  category   TEXT NOT NULL DEFAULT '',
  info       TEXT NOT NULL DEFAULT '{}',
  has_name   INTEGER NOT NULL DEFAULT 0,
  has_info   INTEGER NOT NULL DEFAULT 0,
  edited     INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (lang, id)
);
CREATE INDEX IF NOT EXISTS idx_%s_name ON %s(lang, name);
CREATE INDEX IF NOT EXISTS idx_%s_cat ON %s(lang, category);
`

// entityDDL 生成某个实体域的建表语句。表名只来自 Kind.table() 的白名单。
func entityDDL(kind Kind) string {
	t := kind.table()
	return fmt.Sprintf(entityTable, t, t, t, t, t)
}

const schema = `
CREATE TABLE IF NOT EXISTS wz_file (
  lang       TEXT NOT NULL,
  path       TEXT NOT NULL,
  size       INTEGER NOT NULL,
  mtime      INTEGER NOT NULL,
  status     TEXT NOT NULL,
  err        TEXT NOT NULL DEFAULT '',
  repaired   INTEGER NOT NULL DEFAULT 0,
  rows       INTEGER NOT NULL DEFAULT 0,
  scanned_at INTEGER NOT NULL,
  ids        TEXT NOT NULL DEFAULT '[]',
  PRIMARY KEY (lang, path)
);
CREATE TABLE IF NOT EXISTS scan_run (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  lang        TEXT NOT NULL DEFAULT '',
  started_at  INTEGER NOT NULL,
  finished_at INTEGER,
  total       INTEGER NOT NULL DEFAULT 0,
  parsed      INTEGER NOT NULL DEFAULT 0,
  failed      INTEGER NOT NULL DEFAULT 0,
  note        TEXT NOT NULL DEFAULT ''
);
`

// Open 打开（或创建）SQLite 库，必要时把旧版无 lang 列的表迁移过来。
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建目录 %s: %w", dir, err)
		}
	}
	d, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	// 单写多读：写操作全部走这条串行连接，避免 SQLITE_BUSY。
	d.SetMaxOpenConns(1)
	db := &DB{sqlDB: d, Path: path}
	if err := db.init(); err != nil {
		d.Close()
		return nil, err
	}
	return db, nil
}

func (d *DB) init() error {
	ctx, cancel := timeout()
	defer cancel()
	if err := d.migrateLegacy(ctx); err != nil {
		return fmt.Errorf("迁移旧库失败: %w", err)
	}
	if _, err := d.sqlDB.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("建表失败: %w", err)
	}
	// 实体表按域各建一张。IF NOT EXISTS，旧库升级即"多出两张空表"，不动 item 数据。
	for _, k := range Kinds {
		if _, err := d.sqlDB.ExecContext(ctx, entityDDL(k)); err != nil {
			return fmt.Errorf("建实体表 %s: %w", k.table(), err)
		}
	}
	// 后加的列：老库（含已扫完的正式库）只补列不清数据，默认值即"未参与溯源/未手工修改"。
	type col struct{ table, name, ddl string }
	for _, c := range []col{
		{"item", "edited", `ALTER TABLE item ADD COLUMN edited INTEGER NOT NULL DEFAULT 0`},
		{"wz_file", "ids", `ALTER TABLE wz_file ADD COLUMN ids TEXT NOT NULL DEFAULT '[]'`},
	} {
		has, err := d.hasColumn(ctx, c.table, c.name)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := d.sqlDB.ExecContext(ctx, c.ddl); err != nil {
			return fmt.Errorf("补列 %s.%s: %w", c.table, c.name, err)
		}
	}
	return nil
}

// hasColumn 判断表里是否已有某列（表不存在时返回 false）。
func (d *DB) hasColumn(ctx context.Context, table, col string) (bool, error) {
	rows, err := d.sqlDB.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return false, err
		}
		if strings.EqualFold(n, col) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// migrateLegacy 处理"首版没有 lang 列"的库：重命名旧表、建新表、按 LegacyLang 回填。
// 索引要先删掉，否则表重命名后同名索引会挡住新表的 CREATE INDEX。
func (d *DB) migrateLegacy(ctx context.Context) error {
	type job struct {
		table   string
		columns string
	}
	jobs := []job{
		{"item", "id,name,descr,category,info,has_name,has_info,updated_at"},
		{"wz_file", "path,size,mtime,status,err,repaired,rows,scanned_at"},
	}
	for _, j := range jobs {
		exists, err := d.hasColumn(ctx, j.table, "lang")
		if err != nil {
			return err
		}
		ok, err := d.tableExists(ctx, j.table)
		if err != nil {
			return err
		}
		if !ok || exists {
			continue
		}
		if _, err := d.sqlDB.ExecContext(ctx, `DROP INDEX IF EXISTS idx_item_name`); err != nil {
			return err
		}
		if _, err := d.sqlDB.ExecContext(ctx, `DROP INDEX IF EXISTS idx_item_cat`); err != nil {
			return err
		}
		old := j.table + "_v1"
		if _, err := d.sqlDB.ExecContext(ctx, `ALTER TABLE `+j.table+` RENAME TO `+old); err != nil {
			return fmt.Errorf("重命名 %s: %w", j.table, err)
		}
		if _, err := d.sqlDB.ExecContext(ctx, schema); err != nil {
			return err
		}
		// item 的 DDL 不在 schema 里（三张实体表由 entityDDL 生成），这里补建被重命名走的那张。
		if j.table == KindItem.table() {
			if _, err := d.sqlDB.ExecContext(ctx, entityDDL(KindItem)); err != nil {
				return err
			}
		}
		insert := fmt.Sprintf(`INSERT OR IGNORE INTO %s(lang,%s) SELECT ?,%s FROM %s`,
			j.table, j.columns, j.columns, old)
		if _, err := d.sqlDB.ExecContext(ctx, insert, LegacyLang); err != nil {
			return fmt.Errorf("回填 %s: %w", j.table, err)
		}
		if _, err := d.sqlDB.ExecContext(ctx, `DROP TABLE `+old); err != nil {
			return err
		}
	}
	// 旧版 scan_run 只有一列 lang 要补；表还不存在时交给建表语句处理。
	if ok, err := d.tableExists(ctx, "scan_run"); err != nil {
		return err
	} else if ok {
		if has, err := d.hasColumn(ctx, "scan_run", "lang"); err != nil {
			return err
		} else if !has {
			if _, err := d.sqlDB.ExecContext(ctx,
				`ALTER TABLE scan_run ADD COLUMN lang TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
		}
	}
	return nil
}

// tableExists 判断表是否已存在。
func (d *DB) tableExists(ctx context.Context, name string) (bool, error) {
	var cnt int
	if err := d.sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = ?`, name).Scan(&cnt); err != nil {
		return false, err
	}
	return cnt > 0, nil
}

// Close 关闭连接。
func (d *DB) Close() error { return d.sqlDB.Close() }

func timeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// Pending 返回指定语言下需要（重新）解析的文件：库里没有记录，或 size/mtime 变了。
func (d *DB) Pending(lang string, recs []FileRec) ([]FileRec, error) {
	ctx, cancel := timeout()
	defer cancel()
	type fp struct {
		size  int64
		mtime int64
	}
	existing := map[string]fp{}
	rows, err := d.sqlDB.QueryContext(ctx, `SELECT path, size, mtime FROM wz_file WHERE lang = ?`, lang)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var v fp
		if err := rows.Scan(&p, &v.size, &v.mtime); err != nil {
			return nil, err
		}
		existing[p] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []FileRec
	for _, r := range recs {
		e, ok := existing[r.Path]
		if !ok || e.size != r.Size || e.mtime != r.ModTime.Unix() {
			out = append(out, r)
		}
	}
	return out, nil
}

// SaveFiles 批量写入文件指纹；失败记录同样入库，便于排查与重跑。
func (d *DB) SaveFiles(lang string, recs []FileRec) error {
	if len(recs) == 0 {
		return nil
	}
	ctx, cancel := timeout()
	defer cancel()
	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO wz_file(lang,path,size,mtime,status,err,repaired,rows,scanned_at,ids)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(lang,path) DO UPDATE SET size=excluded.size, mtime=excluded.mtime, status=excluded.status,
		  err=excluded.err, repaired=excluded.repaired, rows=excluded.rows, scanned_at=excluded.scanned_at,
		  ids=excluded.ids`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range recs {
		status := r.Status
		if status == "" {
			status = "ok"
		}
		ids, err := marshalIDs(r.Kind, r.IDs)
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(lang, r.Path, r.Size, r.ModTime.Unix(), status, r.Err, r.Repaired, r.Rows, time.Now().Unix(), ids); err != nil {
			return fmt.Errorf("写入文件记录 %s: %w", r.Path, err)
		}
	}
	return tx.Commit()
}

// NameArg 是名称侧的入库参数。Kind 为空按物品处理。
// Src 是贡献这条记录的文件相对路径：落库前按 (Kind, ID, Src) 排序，
// 同一个 ID 被多个文件写入时胜者固定，不再随并发完成顺序抖动。
type NameArg struct {
	Kind     Kind
	ID       string
	Name     string
	Descr    string
	Category string
	Src      string
}

// tableSQL 把模板里的 {{t}} 换成实体表名。
// 表名只可能来自 Kind.table() 的白名单映射，因此拼接是安全的。
func tableSQL(tmpl, tbl string) string { return strings.ReplaceAll(tmpl, "{{t}}", tbl) }

const putNamesSQL = `INSERT INTO {{t}}(lang,id,name,descr,category,has_name,updated_at)
	VALUES(?,?,?,?,?,1,?)
	ON CONFLICT(lang,id) DO UPDATE SET
	  name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE {{t}}.name END,
	  descr = CASE WHEN excluded.descr <> '' THEN excluded.descr ELSE {{t}}.descr END,
	  category = CASE WHEN {{t}}.category = '' THEN excluded.category ELSE {{t}}.category END,
	  has_name = 1,
	  updated_at = excluded.updated_at`

// PutNames 写入名称侧（来自 String.wz）。名称为空不覆盖已有名称。
// 一个批次可以混装多个实体域，按 Kind 分流到各自的表。
func (d *DB) PutNames(lang string, names []NameArg) (int, error) {
	if len(names) == 0 {
		return 0, nil
	}
	ctx, cancel := timeout()
	defer cancel()
	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmts := map[string]*sql.Stmt{}
	defer func() {
		for _, s := range stmts {
			s.Close()
		}
	}()
	n := 0
	for _, a := range names {
		if strings.TrimSpace(a.Name) == "" {
			continue
		}
		tbl := a.Kind.table()
		st, ok := stmts[tbl]
		if !ok {
			if st, err = tx.PrepareContext(ctx, tableSQL(putNamesSQL, tbl)); err != nil {
				return 0, err
			}
			stmts[tbl] = st
		}
		if _, err := st.Exec(lang, a.ID, a.Name, a.Descr, a.Category, time.Now().Unix()); err != nil {
			return n, fmt.Errorf("写入名称 %s/%s: %w", tbl, a.ID, err)
		}
		n++
	}
	return n, tx.Commit()
}

// InfoArg 是属性侧的入库参数。Kind 为空按物品处理，Src 见 NameArg。
type InfoArg struct {
	Kind     Kind
	ID       string
	Category string
	Info     map[string]string
	Src      string
}

const putInfosSQL = `INSERT INTO {{t}}(lang,id,category,info,has_info,updated_at)
			VALUES(?,?,?,?,1,?)
			ON CONFLICT(lang,id) DO UPDATE SET
			  info = excluded.info,
			  category = CASE WHEN {{t}}.category = '' THEN excluded.category ELSE {{t}}.category END,
			  has_info = 1,
			  updated_at = excluded.updated_at`

// PutInfos 写入属性侧（来自 Item.wz / Character.wz / Mob.wz / Npc.wz），按 key 合并已有 info。
func (d *DB) PutInfos(lang string, infos []InfoArg) (int, error) {
	if len(infos) == 0 {
		return 0, nil
	}
	ctx, cancel := timeout()
	defer cancel()
	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, a := range infos {
		if len(a.Info) == 0 {
			continue
		}
		tbl := a.Kind.table()
		merged, err := mergeInfo(ctx, tx, tbl, lang, a.ID, a.Info)
		if err != nil {
			return n, err
		}
		_, err = tx.ExecContext(ctx, tableSQL(putInfosSQL, tbl),
			lang, a.ID, a.Category, merged, time.Now().Unix())
		if err != nil {
			return n, fmt.Errorf("写入属性 %s/%s: %w", tbl, a.ID, err)
		}
		n++
	}
	return n, tx.Commit()
}

func mergeInfo(ctx context.Context, tx *sql.Tx, tbl, lang, id string, add map[string]string) (string, error) {
	var cur sql.NullString
	q := tableSQL(`SELECT info FROM {{t}} WHERE lang = ? AND id = ?`, tbl)
	if err := tx.QueryRowContext(ctx, q, lang, id).Scan(&cur); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	out := map[string]string{}
	if cur.Valid && cur.String != "" {
		if err := json.Unmarshal([]byte(cur.String), &out); err != nil {
			out = map[string]string{}
		}
	}
	for k, v := range add {
		out[k] = v
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Query 是检索条件，零值表示不过滤。
type Query struct {
	Kind     Kind     // 实体域，空 = 物品
	Lang     string   // 语言域，必填（空则用 DefaultLang 语义由调用方保证）
	ID       string   // 精确 ID
	IDPrefix string   // ID 前缀，兼容 7 位号段
	Name     string   // 名称模糊匹配
	Category string   // 分类精确匹配
	HasName  *bool    // 是否有名称
	HasInfo  *bool    // 是否有属性
	Wants    []string // 属性 key 存在且等于值，形如 "cash=1"；只支持等值
	Like     []string // 属性 key 的值包含子串，形如 "name=剑"
	GTE      map[string]int64
	LTE      map[string]int64 // 属性数值上界，语义同 GTE
	Has      []string         // 只要求属性 key 存在，不比值
	Limit    int
	Offset   int
	OrderBy  string // id / name / category
	Desc     bool
}

// Search 返回命中行与总数。
func (d *DB) Search(q Query) ([]ItemRow, int, error) {
	ctx, cancel := timeout()
	defer cancel()

	where, args := q.clauses()
	tbl := q.Kind.table()
	base := `FROM ` + tbl + ` ` + where
	var total int
	if err := d.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) `+base, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	orderBy := q.OrderBy
	switch orderBy {
	case "name", "category", "id":
	default:
		orderBy = "id"
	}
	dir := "ASC"
	if q.Desc {
		dir = "DESC"
	}
	rows, err := d.sqlDB.QueryContext(ctx,
		`SELECT lang,id,name,descr,category,info,has_name,has_info,edited,updated_at `+base+
			fmt.Sprintf(` ORDER BY %s %s LIMIT ? OFFSET ?`, orderBy, dir),
		append(args, limit, q.Offset)...)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	out := make([]ItemRow, 0, limit)
	for rows.Next() {
		it, err := scanItem(rows, q.Kind)
		if err != nil {
			return nil, total, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// Get 按实体域 + 语言 + ID 取单条。入参 ID 与库内一致地做零填充归一。
func (d *DB) Get(kind Kind, lang, id string) (ItemRow, bool, error) {
	ctx, cancel := timeout()
	defer cancel()
	id = extract.NormalizeID(id)
	q := tableSQL(`SELECT lang,id,name,descr,category,info,has_name,has_info,edited,updated_at FROM {{t}} WHERE lang = ? AND id = ?`, kind.table())
	row := d.sqlDB.QueryRowContext(ctx, q, lang, id)
	it, err := scanItem(row, kind)
	if errors.Is(err, sql.ErrNoRows) {
		return ItemRow{}, false, nil
	}
	if err != nil {
		return ItemRow{}, false, err
	}
	return it, true, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanItem(s scanner, kind Kind) (ItemRow, error) {
	var (
		it               ItemRow
		info             string
		hasName, hasInfo int
		edited           int
		upd              int64
	)
	err := s.Scan(&it.Lang, &it.ID, &it.Name, &it.Descr, &it.Category, &info, &hasName, &hasInfo, &edited, &upd)
	if err != nil {
		return ItemRow{}, err
	}
	it.Kind = kind
	it.HasName, it.HasInfo, it.Edited = hasName != 0, hasInfo != 0, edited != 0
	it.Updated = time.Unix(upd, 0)
	it.Info = map[string]string{}
	if info != "" {
		if err := json.Unmarshal([]byte(info), &it.Info); err != nil {
			return ItemRow{}, fmt.Errorf("%s %s 的 info 不是合法 JSON: %w", kind, it.ID, err)
		}
	}
	return it, nil
}

// Totals 是库内概况。字段带 json 标签，供内置查询页直接消费。
type Totals struct {
	Kind        Kind       `json:"kind,omitempty"`
	Lang        string     `json:"lang"`
	Items       int        `json:"items"`
	Named       int        `json:"named"`
	WithInfo    int        `json:"withInfo"`
	Complete    int        `json:"complete"`
	Files       int        `json:"files"`
	Failed      int        `json:"failed"`
	LastRunUnix int64      `json:"lastRunUnix"`
	Categories  []CatCount `json:"categories"`
}

// CatCount 是一个分类下的物品数。
type CatCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// LangCount 是一套语言域的规模。
type LangCount struct {
	Lang  string `json:"lang"`
	Items int    `json:"items"`
	Named int    `json:"named"`
}

// Langs 返回库里已有的语言域，按物品数降序。
// Langs 返回某个实体域下已有的语言域，按条数降序。
// 三个域的规模差一个量级（物品 5.6 万、NPC 7 千），所以下拉的计数必须跟着域走。
func (d *DB) Langs(kind Kind) ([]LangCount, error) {
	ctx, cancel := timeout()
	defer cancel()
	rows, err := d.sqlDB.QueryContext(ctx,
		tableSQL(`SELECT lang, COUNT(*), COALESCE(SUM(has_name),0) FROM {{t}} GROUP BY lang ORDER BY COUNT(*) DESC`, kind.table()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LangCount
	for rows.Next() {
		var c LangCount
		if err := rows.Scan(&c.Lang, &c.Items, &c.Named); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Stats 汇总某一实体域 + 语言域的数据。文件计数是语言域级别的，与 kind 无关。
func (d *DB) Stats(kind Kind, lang string) (Totals, error) {
	ctx, cancel := timeout()
	defer cancel()
	tbl := kind.table()
	var t Totals
	t.Lang = lang
	t.Kind = kind
	if err := d.sqlDB.QueryRowContext(ctx,
		tableSQL(`SELECT COUNT(*), COALESCE(SUM(has_name),0), COALESCE(SUM(has_info),0),
			        COALESCE(SUM(has_name*has_info),0) FROM {{t}} WHERE lang = ?`, tbl), lang).
		Scan(&t.Items, &t.Named, &t.WithInfo, &t.Complete); err != nil {
		return t, err
	}
	if err := d.sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),0)
		 FROM wz_file WHERE lang = ?`, lang).
		Scan(&t.Files, &t.Failed); err != nil {
		return t, err
	}
	var last sql.NullInt64
	if err := d.sqlDB.QueryRowContext(ctx,
		`SELECT MAX(started_at) FROM scan_run WHERE lang = ?`, lang).Scan(&last); err == nil && last.Valid {
		t.LastRunUnix = last.Int64
	}
	cats, err := d.queryCats(ctx, lang, tableSQL(`SELECT COALESCE(NULLIF(category,''),'`+NoCategoryName+`') c, COUNT(*) FROM {{t}} WHERE lang = ? GROUP BY c ORDER BY COUNT(*) DESC LIMIT 12`, tbl))
	if err != nil {
		return t, err
	}
	t.Categories = cats
	return t, nil
}

// Categories 返回某实体域 + 语言域下的全部分类及条数，按数量降序。
func (d *DB) Categories(kind Kind, lang string) ([]CatCount, error) {
	ctx, cancel := timeout()
	defer cancel()
	return d.queryCats(ctx, lang, tableSQL(`SELECT COALESCE(NULLIF(category,''),'`+NoCategoryName+`') c, COUNT(*) FROM {{t}} WHERE lang = ? GROUP BY c ORDER BY COUNT(*) DESC`, kind.table()))
}

// AttrCount 是一个属性键在该语言域内出现的物品数。
type AttrCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// AttrKeys 统计库内实际出现的信息属性键，按覆盖面降序。
// 用 json_each 展开 info，56k 行量级在百毫秒内；limit<=0 表示不限。
func (d *DB) AttrKeys(kind Kind, lang string, limit int) ([]AttrCount, error) {
	ctx, cancel := timeout()
	defer cancel()
	tbl := kind.table()
	q := tableSQL(`SELECT j.key, COUNT(*) FROM {{t}}, json_each({{t}}.info) j WHERE {{t}}.lang = ? GROUP BY j.key ORDER BY COUNT(*) DESC, j.key`, tbl)
	args := []any{lang}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := d.sqlDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttrCount
	for rows.Next() {
		var a AttrCount
		if err := rows.Scan(&a.Key, &a.Count); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (d *DB) queryCats(ctx context.Context, lang, sqlText string) ([]CatCount, error) {
	rows, err := d.sqlDB.QueryContext(ctx, sqlText, lang)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CatCount
	for rows.Next() {
		var c CatCount
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// StartRun 记录一次扫描开始，返回行 id。
func (d *DB) StartRun(lang string, total int, note string) (int64, error) {
	ctx, cancel := timeout()
	defer cancel()
	res, err := d.sqlDB.ExecContext(ctx, `INSERT INTO scan_run(lang,started_at,total,note) VALUES(?,?,?,?)`,
		lang, time.Now().Unix(), total, note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishRun 收尾一次扫描。
func (d *DB) FinishRun(id int64, parsed, failed int) error {
	ctx, cancel := timeout()
	defer cancel()
	_, err := d.sqlDB.ExecContext(ctx,
		`UPDATE scan_run SET finished_at = ?, parsed = ?, failed = ? WHERE id = ?`,
		time.Now().Unix(), parsed, failed, id)
	return err
}

// DeleteMissing 清理某语言域下磁盘已不存在的文件记录，返回删除数。
// 记录里有 IDs 溯源时，会连带清掉这些文件独占的名称/属性（见 DeleteFiles）。
func (d *DB) DeleteMissing(lang string, alive []string) (int, error) {
	ctx, cancel := timeout()
	defer cancel()
	have := make(map[string]bool, len(alive))
	for _, p := range alive {
		have[p] = true
	}
	rows, err := d.sqlDB.QueryContext(ctx, `SELECT path FROM wz_file WHERE lang = ?`, lang)
	if err != nil {
		return 0, err
	}
	var gone []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, err
		}
		if !have[p] {
			gone = append(gone, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(gone) == 0 {
		return 0, nil
	}
	n, _, err := d.DeleteFiles(lang, gone, true)
	return n, err
}

// jsonPath 把 info 的 key 包成带引号的 JSON 路径。
// 必须加双引号：真实 key 里有 "icon.width"（点会被当成嵌套路径）和 "@indent" 这类属性键。
func jsonPath(k string) string {
	return `$."` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(k) + `"`
}

func (q Query) clauses() (string, []any) {
	var parts []string
	var args []any
	add := func(s string, a ...any) {
		parts = append(parts, s)
		args = append(args, a...)
	}
	add(`lang = ?`, q.Lang)
	if q.ID != "" {
		add(`id = ?`, extract.NormalizeID(q.ID))
	}
	if q.IDPrefix != "" {
		// 库里 ID 统一是 8 位零填充，用户手输的往往是原始 7 位号段前缀，
		// 因此 "4000" 要同时命中 8 位的 4000xxxx 和补零后的 0400xxxx。
		prefixes := []string{q.IDPrefix}
		if len(q.IDPrefix) < idPadLen {
			prefixes = append(prefixes, "0"+q.IDPrefix)
		}
		ors := make([]string, 0, len(prefixes))
		for _, p := range prefixes {
			ors = append(ors, `(id >= ? AND id < ?)`)
			args = append(args, p, prefixUpper(p))
		}
		parts = append(parts, "("+strings.Join(ors, " OR ")+")")
	}
	if q.Name != "" {
		add(`name LIKE ?`, "%"+q.Name+"%")
	}
	if q.Category != "" {
		if q.Category == NoCategoryName {
			add(`category = ''`)
		} else {
			add(`category = ?`, q.Category)
		}
	}
	if q.HasName != nil {
		add(`has_name = ?`, boolInt(*q.HasName))
	}
	if q.HasInfo != nil {
		add(`has_info = ?`, boolInt(*q.HasInfo))
	}
	for _, w := range q.Wants {
		k, v, ok := strings.Cut(w, "=")
		if !ok {
			continue
		}
		add(`json_extract(info, ?) = ?`, jsonPath(k), v)
	}
	for _, w := range q.Like {
		k, v, ok := strings.Cut(w, "=")
		if !ok {
			continue
		}
		add(`json_extract(info, ?) LIKE ?`, jsonPath(k), "%"+v+"%")
	}
	// 数值比较一律先要求键存在：否则缺失的键会被 CAST 成 0，
	// 让 "reqLevel<=5" 命中所有没有等级要求的物品。
	for k, v := range q.GTE {
		add(`json_extract(info, ?) IS NOT NULL AND CAST(json_extract(info, ?) AS INTEGER) >= ?`, jsonPath(k), jsonPath(k), v)
	}
	for k, v := range q.LTE {
		add(`json_extract(info, ?) IS NOT NULL AND CAST(json_extract(info, ?) AS INTEGER) <= ?`, jsonPath(k), jsonPath(k), v)
	}
	for _, k := range q.Has {
		add(`json_extract(info, ?) IS NOT NULL`, jsonPath(k))
	}
	return ` WHERE ` + strings.Join(parts, " AND "), args
}

func prefixUpper(p string) string {
	b := []byte(p)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xff {
			b[i]++
			return string(b[:i+1])
		}
	}
	return p
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
