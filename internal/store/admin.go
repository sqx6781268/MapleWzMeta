// 本文件是管理侧的写接口：文件指纹比对、按文件精确覆盖、失效贡献清理与手工修正。
//
// 与扫描侧的 PutNames/PutInfos 区别在覆盖语义：
//   - 扫描是"只增不减"（新值为空不覆盖、info 按键合并），适合首次全量导入；
//   - 管理是"以文件为准"（该文件解析出什么就是什么，解析不到的贡献清掉），
//     前提是 wz_file.ids 里有溯源。旧库该列为空，此时退化为只覆盖不清理。
//
// 溯源清单里的每一项是 `kind:id`（如 `mob:00000021`）。带实体域前缀是必需的：
// 物品/怪物/NPC 共用一个 8 位补零 ID 空间，同一个 `01110100` 既是戒指也是绿蘑菇，
// 不带域就没法判断"清掉这个文件的贡献"该动哪张表。早于该格式的库里的裸 ID 一律按物品解释。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sqx6781268/MapleWzMeta/internal/extract"
)

// 贡献侧标识。一个文件只属一侧，与 scan 的分流判据一致。
const (
	SideName = "name"
	SideInfo = "info"
)

// SideOfPath 按相对路径判断文件的贡献侧。
func SideOfPath(rel string) string {
	if strings.HasPrefix(rel, "String.wz/") {
		return SideName
	}
	return SideInfo
}

// sideSQL 是归属统计里的侧过滤条件。
func sideSQL(side string) string {
	if side == SideName {
		return `path LIKE 'String.wz/%'`
	}
	return `path NOT LIKE 'String.wz/%'`
}

// IDRef 是一条溯源：实体域 + 8 位 ID。
type IDRef struct {
	Kind Kind
	ID   string
}

// String 是它在 wz_file.ids 里的存储形态。
func (r IDRef) String() string { return tokenOf(r.Kind, r.ID) }

// tokenOf 拼出溯源串。
func tokenOf(kind Kind, id string) string { return string(kind.table()) + ":" + id }

// parseToken 解析溯源串；没有前缀的旧数据按物品解释。
func parseToken(s string) IDRef {
	k, id, ok := strings.Cut(s, ":")
	if !ok {
		return IDRef{Kind: KindItem, ID: s}
	}
	kind, _ := ParseKind(k)
	return IDRef{Kind: kind, ID: id}
}

func marshalIDs(kind Kind, ids []string) (string, error) {
	if len(ids) == 0 {
		return "[]", nil
	}
	toks := make([]string, 0, len(ids))
	for _, id := range ids {
		toks = append(toks, tokenOf(kind, id))
	}
	b, err := json.Marshal(toks)
	if err != nil {
		return "", fmt.Errorf("写入文件 ID 溯源失败: %w", err)
	}
	return string(b), nil
}

func unmarshalIDs(s string) []IDRef {
	var out []string
	if s == "" {
		return nil
	}
	_ = json.Unmarshal([]byte(s), &out)
	toks := make([]IDRef, 0, len(out))
	for _, t := range out {
		toks = append(toks, parseToken(t))
	}
	return toks
}

// DiffTotals 是四类指纹比对结果的计数。
type DiffTotals struct {
	New        int `json:"new"`
	Changed    int `json:"changed"`
	Gone       int `json:"gone"`
	Unchanged  int `json:"unchanged"`
	TotalDisk  int `json:"totalDisk"`
	TotalStore int `json:"totalStore"`
}

// FileDiffRow 是一条比对明细。
type FileDiffRow struct {
	Path      string    `json:"path"`
	State     string    `json:"state"` // new / changed / gone
	Side      string    `json:"side"`
	DiskSize  int64     `json:"diskSize"`
	DiskMtime time.Time `json:"diskMtime,omitzero"`
	DbSize    int64     `json:"dbSize"`
	DbMtime   time.Time `json:"dbMtime,omitzero"`
	DbRows    int       `json:"dbRows"`
	DbIDs     int       `json:"dbIds"`
	Status    string    `json:"status"`
	Err       string    `json:"err,omitempty"`
}

// DiffFiles 把磁盘指纹与库内指纹对齐。disk 来自 scan 的文件采集（已按路径排序）。
// state 非空时只回该状态的明细；state 为空时回"异常三类"（不含 unchanged）；
// limit>0 时截断明细，但计数始终是全量的。
func (d *DB) DiffFiles(lang string, disk []FileRec, state string, limit int) (DiffTotals, []FileDiffRow, error) {
	ctx, cancel := timeout()
	defer cancel()
	stored, err := d.fileMap(ctx, lang)
	if err != nil {
		return DiffTotals{}, nil, err
	}
	tot := DiffTotals{TotalDisk: len(disk), TotalStore: len(stored)}
	seen := map[string]bool{}
	var out []FileDiffRow
	push := func(r FileDiffRow) {
		if state != "" {
			if r.State != state {
				return
			}
		} else if r.State == "unchanged" {
			return
		}
		if limit > 0 && len(out) >= limit {
			return
		}
		out = append(out, r)
	}
	for _, e := range disk {
		seen[e.Path] = true
		s, ok := stored[e.Path]
		row := FileDiffRow{
			Path: e.Path, Side: SideOfPath(e.Path),
			DiskSize: e.Size, DiskMtime: e.ModTime,
			DbSize: s.Size, DbMtime: s.MTime, DbRows: s.Rows, DbIDs: len(s.IDs),
			Status: s.Status, Err: s.Err,
		}
		switch {
		case !ok:
			tot.New++
			row.State = "new"
		case s.Size != e.Size || s.MTime.Unix() != e.ModTime.Unix():
			tot.Changed++
			row.State = "changed"
		default:
			tot.Unchanged++
			row.State = "unchanged"
		}
		push(row)
	}
	for p, s := range stored {
		if seen[p] {
			continue
		}
		tot.Gone++
		push(FileDiffRow{
			Path: p, State: "gone", Side: SideOfPath(p),
			DbSize: s.Size, DbMtime: s.MTime, DbRows: s.Rows, DbIDs: len(s.IDs),
			Status: s.Status, Err: s.Err,
		})
	}
	return tot, out, nil
}

type storedFile struct {
	Size   int64
	MTime  time.Time
	Rows   int
	Status string
	Err    string
	IDs    []IDRef
}

func (d *DB) fileMap(ctx context.Context, lang string) (map[string]storedFile, error) {
	rows, err := d.sqlDB.QueryContext(ctx,
		`SELECT path,size,mtime,rows,status,err,ids FROM wz_file WHERE lang = ?`, lang)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]storedFile{}
	for rows.Next() {
		var p, raw string
		var s storedFile
		var mt int64
		if err := rows.Scan(&p, &s.Size, &mt, &s.Rows, &s.Status, &s.Err, &raw); err != nil {
			return nil, err
		}
		s.IDs = unmarshalIDs(raw)
		s.MTime = time.Unix(mt, 0)
		out[p] = s
	}
	return out, rows.Err()
}

// SelectedPaths 返回比对明细里属于 selector（形如 "changed" 或 "new+changed"）的路径，
// 供"一键重载该类"使用。
func SelectedPaths(rows []FileDiffRow, selector string) []string {
	want := map[string]bool{}
	for _, s := range strings.Split(selector, "+") {
		want[strings.TrimSpace(s)] = true
	}
	var out []string
	for _, r := range rows {
		if want[r.State] {
			out = append(out, r.Path)
		}
	}
	return out
}

// FileIDs 取一个文件当前登记的贡献；库内没有溯源时返回空。
func (d *DB) FileIDs(lang, path string) ([]IDRef, error) {
	ctx, cancel := timeout()
	defer cancel()
	var raw string
	err := d.sqlDB.QueryRowContext(ctx, `SELECT ids FROM wz_file WHERE lang = ? AND path = ?`, lang, path).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return unmarshalIDs(raw), nil
}

// FileIDsByPaths 一次取多个文件的贡献清单，键为路径。
func (d *DB) FileIDsByPaths(lang string, paths []string) (map[string][]IDRef, error) {
	out := map[string][]IDRef{}
	if len(paths) == 0 {
		return out, nil
	}
	ctx, cancel := timeout()
	defer cancel()
	for _, chunk := range chunked(paths, 500) {
		h := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := append([]any{lang}, toArgs(chunk)...)
		rows, err := d.sqlDB.QueryContext(ctx,
			`SELECT path, ids FROM wz_file WHERE lang = ? AND path IN (`+h+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p, raw string
			if err := rows.Scan(&p, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			out[p] = unmarshalIDs(raw)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// idOwners 统计该语言域内每条溯源被多少个文件认领（exclude 里的路径不计）。
// 键是 `kind:id` 串，因此"物品的 01110100"与"怪物的 01110100"各自计数，互不遮挡。
// 用于"清掉某文件的贡献前先确认没有别的文件也在写这个 ID"。
func (d *DB) idOwners(ctx context.Context, lang, side string, exclude map[string]bool) (map[string]int, error) {
	rows, err := d.sqlDB.QueryContext(ctx,
		`SELECT path, ids FROM wz_file WHERE lang = ? AND `+sideSQL(side), lang)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var p, raw string
		if err := rows.Scan(&p, &raw); err != nil {
			return nil, err
		}
		if exclude[p] {
			continue
		}
		for _, t := range unmarshalIDs(raw) {
			out[t.String()]++
		}
	}
	return out, rows.Err()
}

// EditedIDs 返回其中被手工修改过的 ID 集合，用于重载时跳过。查的是该实体域自己的表。
func (d *DB) EditedIDs(kind Kind, lang string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	ctx, cancel := timeout()
	defer cancel()
	for _, chunk := range chunked(ids, 500) {
		h := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := append([]any{lang}, toArgs(chunk)...)
		rows, err := d.sqlDB.QueryContext(ctx,
			tableSQL(`SELECT id FROM {{t}} WHERE lang = ? AND edited = 1 AND id IN (`+h+`)`, kind.table()), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

const putNamesForceSQL = `INSERT INTO {{t}}(lang,id,name,descr,category,has_name,updated_at)
		VALUES(?,?,?,?,?,1,?)
		ON CONFLICT(lang,id) DO UPDATE SET
		  name = excluded.name,
		  descr = excluded.descr,
		  category = CASE WHEN excluded.category <> '' THEN excluded.category ELSE {{t}}.category END,
		  has_name = 1,
		  edited = 0,
		  updated_at = excluded.updated_at`

// PutNamesForce 以文件为准覆盖名称侧：新值直接写入（不再"空值不覆盖"）。
// skipEdited 为 true 时跳过管理页手工改过的行；显式覆盖成功的行会清掉 edited 标记。
func (d *DB) PutNamesForce(lang string, names []NameArg, skipEdited bool) (int, error) {
	rows := make([]NameArg, 0, len(names))
	if err := d.filterEdited(lang, names, skipEdited, &rows); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
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
	for _, a := range rows {
		tbl := a.Kind.table()
		st, ok := stmts[tbl]
		if !ok {
			if st, err = tx.PrepareContext(ctx, tableSQL(putNamesForceSQL, tbl)); err != nil {
				return 0, err
			}
			stmts[tbl] = st
		}
		if _, err := st.Exec(lang, a.ID, a.Name, a.Descr, a.Category, time.Now().Unix()); err != nil {
			return n, fmt.Errorf("覆盖名称 %s/%s: %w", tbl, a.ID, err)
		}
		n++
	}
	return n, tx.Commit()
}

const putInfosForceSQL = `INSERT INTO {{t}}(lang,id,category,info,has_info,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(lang,id) DO UPDATE SET
		  info = excluded.info,
		  category = CASE WHEN excluded.category <> '' THEN excluded.category ELSE {{t}}.category END,
		  has_info = excluded.has_info,
		  edited = 0,
		  updated_at = excluded.updated_at`

// PutInfosForce 以文件为准覆盖属性侧：info 整包替换，不再按键合并。
// 假定"一个 ID 的完整属性来自同一个文件"，这与 Character.wz/Mob.wz/Npc.wz 单文件单实体、
// Item.wz 分组文件内 ID 不重复的实测结构一致；跨文件同 ID 由 ClearSide 的归属统计兜底。
func (d *DB) PutInfosForce(lang string, infos []InfoArg, skipEdited bool) (int, error) {
	rows := make([]InfoArg, 0, len(infos))
	if err := d.filterEditedInfo(lang, infos, skipEdited, &rows); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
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
	for _, a := range rows {
		tbl := a.Kind.table()
		st, ok := stmts[tbl]
		if !ok {
			if st, err = tx.PrepareContext(ctx, tableSQL(putInfosForceSQL, tbl)); err != nil {
				return 0, err
			}
			stmts[tbl] = st
		}
		b, err := json.Marshal(a.Info)
		if err != nil {
			return n, err
		}
		has := boolInt(len(a.Info) > 0)
		if _, err := st.Exec(lang, a.ID, a.Category, string(b), has, time.Now().Unix()); err != nil {
			return n, fmt.Errorf("覆盖属性 %s/%s: %w", tbl, a.ID, err)
		}
		n++
	}
	return n, tx.Commit()
}

// filterEdited 挑出未被手工修改的名称行。手工标记是按实体域存的，故先按 Kind 分组再查。
func (d *DB) filterEdited(lang string, in []NameArg, skip bool, out *[]NameArg) error {
	if !skip {
		*out = append(*out, in...)
		return nil
	}
	byKind := map[Kind][]string{}
	for _, a := range in {
		byKind[a.Kind] = append(byKind[a.Kind], a.ID)
	}
	bad := map[Kind]map[string]bool{}
	for k, ids := range byKind {
		m, err := d.EditedIDs(k, lang, ids)
		if err != nil {
			return err
		}
		bad[k] = m
	}
	for _, a := range in {
		if !bad[a.Kind][a.ID] {
			*out = append(*out, a)
		}
	}
	return nil
}

// filterEditedInfo 同 filterEdited，作用于属性行。
func (d *DB) filterEditedInfo(lang string, in []InfoArg, skip bool, out *[]InfoArg) error {
	if !skip {
		*out = append(*out, in...)
		return nil
	}
	byKind := map[Kind][]string{}
	for _, a := range in {
		byKind[a.Kind] = append(byKind[a.Kind], a.ID)
	}
	bad := map[Kind]map[string]bool{}
	for k, ids := range byKind {
		m, err := d.EditedIDs(k, lang, ids)
		if err != nil {
			return err
		}
		bad[k] = m
	}
	for _, a := range in {
		if !bad[a.Kind][a.ID] {
			*out = append(*out, a)
		}
	}
	return nil
}

// ClearSide 清掉一批溯源在某一侧的数据，返回实际清除行数。
// 会先剔除仍有其他文件认领的条目（owner 归属统计），再按 skipEdited 保护手工修改。
// exceptPath 是"正在重载的那个文件"自身，统计归属时排除它。
func (d *DB) ClearSide(lang string, refs []IDRef, side, exceptPath string, skipEdited bool) (int, error) {
	if len(refs) == 0 {
		return 0, nil
	}
	ctx, cancel := timeout()
	defer cancel()
	owners, err := d.idOwners(ctx, lang, side, map[string]bool{exceptPath: true})
	if err != nil {
		return 0, err
	}
	return d.clearRefs(ctx, lang, refs, side, owners, skipEdited)
}

// clearRefs 是清除动作的公共实现：先按归属过滤，再按实体域分组落到各自的表。
// 归属判断用 `kind:id` 串，因此物品行不会因为同值的怪物溯源而被误清。
func (d *DB) clearRefs(ctx context.Context, lang string, refs []IDRef, side string, owners map[string]int, skipEdited bool) (int, error) {
	set := `name='',descr='',has_name=0`
	if side == SideInfo {
		set = `info='{}',has_info=0`
	}
	grouped := map[Kind][]string{}
	seen := map[string]bool{}
	for _, r := range refs {
		key := r.String()
		if seen[key] || owners[key] > 0 {
			continue
		}
		seen[key] = true
		grouped[r.Kind] = append(grouped[r.Kind], r.ID)
	}
	var cleared int
	for kind, ids := range grouped {
		if skipEdited {
			bad, err := d.EditedIDs(kind, lang, ids)
			if err != nil {
				return cleared, err
			}
			kept := ids[:0]
			for _, id := range ids {
				if !bad[id] {
					kept = append(kept, id)
				}
			}
			ids = kept
		}
		for _, chunk := range chunked(ids, 500) {
			h := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
			args := append([]any{time.Now().Unix(), lang}, toArgs(chunk)...)
			q := tableSQL(fmt.Sprintf(`UPDATE {{t}} SET %s, updated_at = ? WHERE lang = ? AND id IN (%s)`, set, h), kind.table())
			res, err := d.sqlDB.ExecContext(ctx, q, args...)
			if err != nil {
				return cleared, err
			}
			if n, err := res.RowsAffected(); err == nil {
				cleared += int(n)
			}
		}
	}
	return cleared, nil
}

// DeleteFiles 删除若干文件记录（磁盘上已不存在的），并清掉它们独有的贡献。
// 返回删除的文件数与清除的实体行数。
func (d *DB) DeleteFiles(lang string, paths []string, skipEdited bool) (int, int, error) {
	if len(paths) == 0 {
		return 0, 0, nil
	}
	ctx, cancel := timeout()
	defer cancel()
	byPath, err := d.FileIDsByPaths(lang, paths)
	if err != nil {
		return 0, 0, err
	}
	exclude := map[string]bool{}
	var nameRefs, infoRefs []IDRef
	for _, p := range paths {
		exclude[p] = true
		if SideOfPath(p) == SideName {
			nameRefs = append(nameRefs, byPath[p]...)
		} else {
			infoRefs = append(infoRefs, byPath[p]...)
		}
	}
	var cleared int
	for _, side := range []string{SideName, SideInfo} {
		owners, err := d.idOwners(ctx, lang, side, exclude)
		if err != nil {
			return 0, 0, err
		}
		refs := nameRefs
		if side == SideInfo {
			refs = infoRefs
		}
		n, err := d.clearRefs(ctx, lang, refs, side, owners, skipEdited)
		if err != nil {
			return 0, 0, err
		}
		cleared += n
	}
	for _, chunk := range chunked(paths, 500) {
		h := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := append([]any{lang}, toArgs(chunk)...)
		if _, err := d.sqlDB.ExecContext(ctx,
			`DELETE FROM wz_file WHERE lang = ? AND path IN (`+h+`)`, args...); err != nil {
			return 0, 0, err
		}
	}
	return len(paths), cleared, nil
}

// ItemEdit 是管理页手工修正的一行；Info 为 nil 表示不改属性列。
type ItemEdit struct {
	Name     string            `json:"name"`
	Descr    string            `json:"descr"`
	Category string            `json:"category"`
	Info     map[string]string `json:"info"`
	Edited   bool              `json:"edited"`
}

// UpdateItem 手工写入一条实体行，并打上 edited 标记；返回是否命中已有行。
// 手工行不会被增量扫描覆盖（扫描走的是"空值不覆盖 + 按键合并"，本来就可能改到它，
// 因此重载类端点默认带 skipEdited）。
func (d *DB) UpdateItem(kind Kind, lang, id string, e ItemEdit) (bool, error) {
	ctx, cancel := timeout()
	defer cancel()
	infoJSON := ""
	if e.Info != nil {
		b, err := json.Marshal(e.Info)
		if err != nil {
			return false, err
		}
		infoJSON = string(b)
	}
	var cur sql.NullString
	tbl := kind.table()
	id = extract.NormalizeID(id)
	err := d.sqlDB.QueryRowContext(ctx, tableSQL(`SELECT info FROM {{t}} WHERE lang = ? AND id = ?`, tbl), lang, id).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if infoJSON == "" {
		infoJSON = cur.String
		if infoJSON == "" {
			infoJSON = "{}"
		}
	}
	_, err = d.sqlDB.ExecContext(ctx,
		tableSQL(`UPDATE {{t}} SET name = ?, descr = ?, category = ?, info = ?,
		   has_name = ?, has_info = ?, edited = ?, updated_at = ? WHERE lang = ? AND id = ?`, tbl),
		e.Name, e.Descr, e.Category, infoJSON, boolInt(e.Name != ""), boolInt(infoJSON != "{}"),
		boolInt(e.Edited), time.Now().Unix(), lang, id)
	if err != nil {
		return false, err
	}
	return true, nil
}

// DeleteItem 删除一条实体行，返回是否存在。
func (d *DB) DeleteItem(kind Kind, lang, id string) (bool, error) {
	ctx, cancel := timeout()
	defer cancel()
	id = extract.NormalizeID(id)
	res, err := d.sqlDB.ExecContext(ctx,
		tableSQL(`DELETE FROM {{t}} WHERE lang = ? AND id = ?`, kind.table()), lang, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// FileRow 是管理页文件列表的一行。
type FileRow struct {
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"mtime,omitzero"`
	Status    string    `json:"status"`
	Err       string    `json:"err,omitempty"`
	Repaired  int       `json:"repaired"`
	Rows      int       `json:"rows"`
	IDs       int       `json:"ids"`
	ScannedAt time.Time `json:"scannedAt,omitzero"`
}

// ListFiles 按路径子串/状态过滤浏览文件记录，按路径升序。
func (d *DB) ListFiles(lang, q, status string, limit, offset int) ([]FileRow, int, error) {
	ctx, cancel := timeout()
	defer cancel()
	where := []string{"lang = ?"}
	args := []any{lang}
	if q != "" {
		where = append(where, `path LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(q)+"%")
	}
	switch status {
	case "ok", "failed":
		where = append(where, `status = ?`)
		args = append(args, status)
	}
	w := " WHERE " + strings.Join(where, " AND ")
	var total int
	if err := d.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM wz_file`+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := d.sqlDB.QueryContext(ctx,
		`SELECT path,size,mtime,status,err,repaired,rows,json_array_length(ids),scanned_at FROM wz_file`+w+
			` ORDER BY path LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	out := make([]FileRow, 0, limit)
	for rows.Next() {
		var f FileRow
		var mt, sc int64
		if err := rows.Scan(&f.Path, &f.Size, &mt, &f.Status, &f.Err, &f.Repaired, &f.Rows, &f.IDs, &sc); err != nil {
			return nil, total, err
		}
		f.ModTime, f.ScannedAt = time.Unix(mt, 0), time.Unix(sc, 0)
		out = append(out, f)
	}
	return out, total, rows.Err()
}

// RunRow 是一次扫描历史。
type RunRow struct {
	ID         int64     `json:"id"`
	Lang       string    `json:"lang"`
	StartedAt  time.Time `json:"startedAt,omitzero"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
	Total      int       `json:"total"`
	Parsed     int       `json:"parsed"`
	Failed     int       `json:"failed"`
	Note       string    `json:"note"`
}

// ListRuns 返回最近 n 次扫描，按开始时间降序。
func (d *DB) ListRuns(lang string, n int) ([]RunRow, error) {
	ctx, cancel := timeout()
	defer cancel()
	if n <= 0 || n > 200 {
		n = 30
	}
	rows, err := d.sqlDB.QueryContext(ctx,
		`SELECT id,lang,started_at,finished_at,total,failed,parsed,note FROM scan_run
		 WHERE lang = ? OR lang = '' ORDER BY started_at DESC LIMIT ?`, lang, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunRow
	for rows.Next() {
		var (
			r     RunRow
			start int64
			end   sql.NullInt64
		)
		if err := rows.Scan(&r.ID, &r.Lang, &start, &end, &r.Total, &r.Failed, &r.Parsed, &r.Note); err != nil {
			return nil, err
		}
		r.StartedAt = time.Unix(start, 0)
		if end.Valid {
			r.FinishedAt = time.Unix(end.Int64, 0)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EditedCount 给出某实体域 + 语言域下手工改过的行数，用来在管理页提醒"重载会跳过这些"。
func (d *DB) EditedCount(kind Kind, lang string) (int, error) {
	ctx, cancel := timeout()
	defer cancel()
	var n int
	err := d.sqlDB.QueryRowContext(ctx,
		tableSQL(`SELECT COUNT(*) FROM {{t}} WHERE lang = ? AND edited = 1`, kind.table()), lang).Scan(&n)
	return n, err
}

// Sources 是一条实体的来源文件，按贡献侧拆开：名称来自哪张 `String.wz` 表、
// 属性来自哪个包文件。详情页要分开显示，所以不合成一个列表。
type Sources struct {
	Name []string `json:"stringFiles,omitempty"`
	Info []string `json:"infoFiles,omitempty"`
}

// SourcesBySide 按"域 + ID"反查来源文件。依赖 wz_file.ids 溯源，旧库没有溯源时返回空。
// 单次查询要展开 5.5 万行 JSON（约 0.25s），只在详情这类单条场景调用，别放进列表接口。
func (d *DB) SourcesBySide(kind Kind, lang, id string) (Sources, error) {
	ctx, cancel := timeout()
	defer cancel()
	id = extract.NormalizeID(id)
	cond := `j.value = ?`
	args := []any{lang, tokenOf(kind, id)}
	if kind == KindItem {
		cond = `(j.value = ? OR j.value = ?)`
		args = append(args, id)
	}
	rows, err := d.sqlDB.QueryContext(ctx,
		`SELECT wz_file.path FROM wz_file, json_each(wz_file.ids) j WHERE wz_file.lang = ? AND `+cond+` ORDER BY wz_file.path`,
		args...)
	if err != nil {
		return Sources{}, err
	}
	defer rows.Close()
	var out Sources
	seen := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return out, err
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		if SideOfPath(p) == SideName {
			out.Name = append(out.Name, p)
		} else {
			out.Info = append(out.Info, p)
		}
	}
	return out, rows.Err()
}

// SourcesOf 反查一个 ID 是由哪些文件贡献的（名称侧与属性侧合成一个升序清单）。
func (d *DB) SourcesOf(kind Kind, lang, id string) ([]string, error) {
	s, err := d.SourcesBySide(kind, lang, id)
	if err != nil {
		return nil, err
	}
	return append(append([]string{}, s.Name...), s.Info...), nil
}

// HasProvenance 判断该语言域是否已有文件级 ID 溯源。
// 早于该功能的库只有指纹没有 ids，此时重载只能覆盖解析到的字段，清不掉历史贡献。
func (d *DB) HasProvenance(lang string) (bool, error) {
	ctx, cancel := timeout()
	defer cancel()
	var n int
	err := d.sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wz_file WHERE lang = ? AND ids <> '[]'`, lang).Scan(&n)
	return n > 0, err
}

// StaleIDs 给出"这个文件上次贡献过、这次解析不到了"的溯源清单，供覆盖式重载清理。
// cur 是本次该文件产出的裸 ID，kind 是该文件所属实体域。
func StaleIDs(prev []IDRef, kind Kind, cur []string) []IDRef {
	now := make(map[string]bool, len(cur))
	for _, id := range cur {
		now[tokenOf(kind, id)] = true
	}
	var out []IDRef
	for _, r := range prev {
		if !now[r.String()] {
			out = append(out, r)
		}
	}
	return out
}

func chunked(in []string, n int) [][]string {
	if n <= 0 {
		n = 500
	}
	var out [][]string
	for i := 0; i < len(in); i += n {
		end := i + n
		if end > len(in) {
			end = len(in)
		}
		out = append(out, in[i:end])
	}
	return out
}

func toArgs(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// escapeLike 让路径里的 % 和 _ 按字面量匹配（SQLite 默认无转义符）。
// 这里用反斜杠并在 LIKE 后加 ESCAPE 子句。
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return filepath.ToSlash(r.Replace(s))
}
