// Package store 负责把解析结果落到 SQLite，并提供检索接口。
//
// 只用标准库 database/sql + 纯 Go 驱动 modernc.org/sqlite：本机没有 gcc，
// 任何需要 cgo 的驱动（mattn/go-sqlite3）都编译不了，这里刻意避开。
//
// 数据按语言分域：item/wz_file 的主键都是 (lang, ...)，同一 ID 在不同语言包下
// 各存一行，互不覆盖；图标与语言无关，不落库。
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

	_ "modernc.org/sqlite"
)

// LegacyLang 是旧库（无 lang 列）迁移时回填的语言标识。
// 现有数据实测来自中文导出包，因此固定为 zh-CN。
const LegacyLang = "zh-CN"

// NoCategoryName 是"分类为空"在统计里的显示名。
// 它不是真实分类，检索时会被翻译回 category = ''。
const NoCategoryName = "(未分类)"

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
}

// ItemRow 是关联后的物品行。字段标签与 /api 返回一致，便于 CLI 导出。
type ItemRow struct {
	Lang     string            `json:"lang"`
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Descr    string            `json:"descr"`
	Category string            `json:"category"`
	Info     map[string]string `json:"info"`
	HasName  bool              `json:"hasName"`
	HasInfo  bool              `json:"hasInfo"`
	Updated  time.Time         `json:"updatedAt,omitzero"`
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
  PRIMARY KEY (lang, path)
);
CREATE TABLE IF NOT EXISTS item (
  lang       TEXT NOT NULL,
  id         TEXT NOT NULL,
  name       TEXT NOT NULL DEFAULT '',
  descr      TEXT NOT NULL DEFAULT '',
  category   TEXT NOT NULL DEFAULT '',
  info       TEXT NOT NULL DEFAULT '{}',
  has_name   INTEGER NOT NULL DEFAULT 0,
  has_info   INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (lang, id)
);
CREATE INDEX IF NOT EXISTS idx_item_name ON item(lang, name);
CREATE INDEX IF NOT EXISTS idx_item_cat ON item(lang, category);
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
	stmt, err := tx.Prepare(`INSERT INTO wz_file(lang,path,size,mtime,status,err,repaired,rows,scanned_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(lang,path) DO UPDATE SET size=excluded.size, mtime=excluded.mtime, status=excluded.status,
		  err=excluded.err, repaired=excluded.repaired, rows=excluded.rows, scanned_at=excluded.scanned_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range recs {
		status := r.Status
		if status == "" {
			status = "ok"
		}
		if _, err := stmt.Exec(lang, r.Path, r.Size, r.ModTime.Unix(), status, r.Err, r.Repaired, r.Rows, time.Now().Unix()); err != nil {
			return fmt.Errorf("写入文件记录 %s: %w", r.Path, err)
		}
	}
	return tx.Commit()
}

// NameArg 是名称侧的入库参数。
type NameArg struct {
	ID       string
	Name     string
	Descr    string
	Category string
}

// PutNames 写入名称侧（来自 String.wz）。名称为空不覆盖已有名称。
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
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO item(lang,id,name,descr,category,has_name,updated_at)
		VALUES(?,?,?,?,?,1,?)
		ON CONFLICT(lang,id) DO UPDATE SET
		  name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE item.name END,
		  descr = CASE WHEN excluded.descr <> '' THEN excluded.descr ELSE item.descr END,
		  category = CASE WHEN item.category = '' THEN excluded.category ELSE item.category END,
		  has_name = 1,
		  updated_at = excluded.updated_at`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	n := 0
	for _, a := range names {
		if strings.TrimSpace(a.Name) == "" {
			continue
		}
		if _, err := stmt.Exec(lang, a.ID, a.Name, a.Descr, a.Category, time.Now().Unix()); err != nil {
			return n, fmt.Errorf("写入名称 %s: %w", a.ID, err)
		}
		n++
	}
	return n, tx.Commit()
}

// InfoArg 是属性侧的入库参数。
type InfoArg struct {
	ID       string
	Category string
	Info     map[string]string
}

// PutInfos 写入属性侧（来自 Item.wz / Character.wz），按 key 合并已有 info。
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
		merged, err := mergeInfo(ctx, tx, lang, a.ID, a.Info)
		if err != nil {
			return n, err
		}
		_, err = tx.Exec(`INSERT INTO item(lang,id,category,info,has_info,updated_at)
			VALUES(?,?,?,?,1,?)
			ON CONFLICT(lang,id) DO UPDATE SET
			  info = excluded.info,
			  category = CASE WHEN item.category = '' THEN excluded.category ELSE item.category END,
			  has_info = 1,
			  updated_at = excluded.updated_at`,
			lang, a.ID, a.Category, merged, time.Now().Unix())
		if err != nil {
			return n, fmt.Errorf("写入属性 %s: %w", a.ID, err)
		}
		n++
	}
	return n, tx.Commit()
}

func mergeInfo(ctx context.Context, tx *sql.Tx, lang, id string, add map[string]string) (string, error) {
	var cur sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT info FROM item WHERE lang = ? AND id = ?`, lang, id).Scan(&cur); err != nil && !errors.Is(err, sql.ErrNoRows) {
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
	base := `FROM item ` + where
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
		`SELECT lang,id,name,descr,category,info,has_name,has_info,updated_at `+base+
			fmt.Sprintf(` ORDER BY %s %s LIMIT ? OFFSET ?`, orderBy, dir),
		append(args, limit, q.Offset)...)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	out := make([]ItemRow, 0, limit)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, total, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// Get 按语言 + ID 取单条。
func (d *DB) Get(lang, id string) (ItemRow, bool, error) {
	ctx, cancel := timeout()
	defer cancel()
	row := d.sqlDB.QueryRowContext(ctx,
		`SELECT lang,id,name,descr,category,info,has_name,has_info,updated_at FROM item WHERE lang = ? AND id = ?`,
		lang, id)
	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ItemRow{}, false, nil
	}
	if err != nil {
		return ItemRow{}, false, err
	}
	return it, true, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanItem(s scanner) (ItemRow, error) {
	var (
		it               ItemRow
		info             string
		hasName, hasInfo int
		upd              int64
	)
	err := s.Scan(&it.Lang, &it.ID, &it.Name, &it.Descr, &it.Category, &info, &hasName, &hasInfo, &upd)
	if err != nil {
		return ItemRow{}, err
	}
	it.HasName, it.HasInfo = hasName != 0, hasInfo != 0
	it.Updated = time.Unix(upd, 0)
	it.Info = map[string]string{}
	if info != "" {
		if err := json.Unmarshal([]byte(info), &it.Info); err != nil {
			return ItemRow{}, fmt.Errorf("物品 %s 的 info 不是合法 JSON: %w", it.ID, err)
		}
	}
	return it, nil
}

// Totals 是库内概况。字段带 json 标签，供内置查询页直接消费。
type Totals struct {
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
func (d *DB) Langs() ([]LangCount, error) {
	ctx, cancel := timeout()
	defer cancel()
	rows, err := d.sqlDB.QueryContext(ctx,
		`SELECT lang, COUNT(*), COALESCE(SUM(has_name),0) FROM item GROUP BY lang ORDER BY COUNT(*) DESC`)
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

// Stats 汇总某一语言域的数据。
func (d *DB) Stats(lang string) (Totals, error) {
	ctx, cancel := timeout()
	defer cancel()
	var t Totals
	t.Lang = lang
	if err := d.sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(has_name),0), COALESCE(SUM(has_info),0),
		        COALESCE(SUM(has_name*has_info),0) FROM item WHERE lang = ?`, lang).
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
	cats, err := d.queryCats(ctx, lang, `SELECT COALESCE(NULLIF(category,''),'`+NoCategoryName+`') c, COUNT(*) FROM item WHERE lang = ? GROUP BY c ORDER BY COUNT(*) DESC LIMIT 12`)
	if err != nil {
		return t, err
	}
	t.Categories = cats
	return t, nil
}

// Categories 返回某语言域下的全部分类及物品数，按数量降序。
func (d *DB) Categories(lang string) ([]CatCount, error) {
	ctx, cancel := timeout()
	defer cancel()
	return d.queryCats(ctx, lang, `SELECT COALESCE(NULLIF(category,''),'`+NoCategoryName+`') c, COUNT(*) FROM item WHERE lang = ? GROUP BY c ORDER BY COUNT(*) DESC`)
}

// AttrCount 是一个属性键在该语言域内出现的物品数。
type AttrCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// AttrKeys 统计库内实际出现的信息属性键，按覆盖面降序。
// 用 json_each 展开 info，56k 行量级在百毫秒内；limit<=0 表示不限。
func (d *DB) AttrKeys(lang string, limit int) ([]AttrCount, error) {
	ctx, cancel := timeout()
	defer cancel()
	q := `SELECT j.key, COUNT(*) FROM item, json_each(item.info) j WHERE item.lang = ? GROUP BY j.key ORDER BY COUNT(*) DESC, j.key`
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
func (d *DB) DeleteMissing(lang string, alive []string) (int, error) {
	ctx, cancel := timeout()
	defer cancel()
	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	have := map[string]bool{}
	for _, p := range alive {
		have[p] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT path FROM wz_file WHERE lang = ?`, lang)
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
	for _, p := range gone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM wz_file WHERE lang = ? AND path = ?`, lang, p); err != nil {
			return 0, err
		}
	}
	return len(gone), tx.Commit()
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
		add(`id = ?`, q.ID)
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
