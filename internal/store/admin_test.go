package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// saveFingerprints 用同一时间戳写文件记录，避免比对里掺进 mtime 差异。
func saveFingerprints(t *testing.T, d *DB, at time.Time, recs ...FileRec) {
	t.Helper()
	for i := range recs {
		recs[i].ModTime = at
		if recs[i].Status == "" {
			recs[i].Status = "ok"
		}
	}
	if err := d.SaveFiles(lang, recs); err != nil {
		t.Fatalf("SaveFiles: %v", err)
	}
}

func TestFileProvenanceRoundTrip(t *testing.T) {
	d := openTestDB(t)
	at := time.Now().Truncate(time.Second)
	saveFingerprints(t, d, at, FileRec{
		Path: "String.wz/Item.img.xml", Size: 10, IDs: []string{"04000000", "02000000"}, Rows: 2,
	})
	ids, err := d.FileIDs(lang, "String.wz/Item.img.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0].ID != "04000000" || ids[0].Kind != KindItem {
		t.Errorf("溯源读回错: %+v", ids)
	}
	if ok, err := d.HasProvenance(lang); err != nil || !ok {
		t.Errorf("HasProvenance 应为 true: %v err=%v", ok, err)
	}
	src, err := d.SourcesOf(KindItem, lang, "02000000")
	if err != nil || len(src) != 1 || src[0] != "String.wz/Item.img.xml" {
		t.Errorf("来源反查错: %v err=%v", src, err)
	}
	// 另一语言域没有溯源，隔离生效。
	if ok, err := d.HasProvenance("en"); err != nil || ok {
		t.Errorf("en 域不该有溯源: %v err=%v", ok, err)
	}
}

func TestDiffFilesClassifiesFourStates(t *testing.T) {
	d := openTestDB(t)
	at := time.Now().Truncate(time.Second)
	saveFingerprints(t, d, at,
		FileRec{Path: "String.wz/Item.img.xml", Size: 10},
		FileRec{Path: "Item.wz/Etc/0430.img.xml", Size: 20},
		FileRec{Path: "Character.wz/Cap/01000000.img.xml", Size: 30},
	)
	disk := []FileRec{
		{Path: "String.wz/Item.img.xml", Size: 10, ModTime: at},            // 未变
		{Path: "Item.wz/Etc/0430.img.xml", Size: 21, ModTime: at},          // 大小变了
		{Path: "Character.wz/Cap/01000001.img.xml", Size: 30, ModTime: at}, // 新增（旧的同名文件被删）
	}
	tot, rows, err := d.DiffFiles(lang, disk, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if tot.Unchanged != 1 || tot.Changed != 1 || tot.New != 1 || tot.Gone != 1 {
		t.Errorf("四类计数错: %+v", tot)
	}
	if tot.TotalDisk != 3 || tot.TotalStore != 3 {
		t.Errorf("总量错: %+v", tot)
	}
	byState := map[string][]string{}
	for _, r := range rows {
		byState[r.State] = append(byState[r.State], r.Path)
	}
	if len(byState["gone"]) != 1 || byState["gone"][0] != "Character.wz/Cap/01000000.img.xml" {
		t.Errorf("已删除清单错: %v", byState["gone"])
	}
	if len(byState["unchanged"]) != 0 {
		t.Errorf("未变不该出现在明细里（state 未过滤时只给异常）: %v", byState["unchanged"])
	}
	// 只要"变更"一类。
	_, only, err := d.DiffFiles(lang, disk, "changed", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Path != "Item.wz/Etc/0430.img.xml" || only[0].Side != SideInfo {
		t.Errorf("按状态过滤错: %+v", only)
	}
	if got := SelectedPaths(rows, "new+changed"); len(got) != 2 {
		t.Errorf("SelectedPaths 错: %v", got)
	}
}

func TestPutInfosForceReplacesAndClearsStale(t *testing.T) {
	d := openTestDB(t)
	at := time.Now().Truncate(time.Second)
	// 首次扫描：合并语义，两个键。
	if _, err := d.PutInfos(lang, []InfoArg{{ID: "04000000", Category: "Etc",
		Info: map[string]string{"slotMax": "100", "price": "1"}}}); err != nil {
		t.Fatal(err)
	}
	saveFingerprints(t, d, at, FileRec{Path: "Item.wz/Etc/0430.img.xml", Size: 20,
		IDs: []string{"04000000", "04000001"}})
	// XML 改过了：price 删掉，只剩 slotMax；04000001 整个不再出现。
	if n, err := d.PutInfosForce(lang, []InfoArg{{ID: "04000000", Category: "Etc",
		Info: map[string]string{"slotMax": "200"}}}, true); err != nil || n != 1 {
		t.Fatalf("PutInfosForce: n=%d err=%v", n, err)
	}
	it, _, err := d.Get(KindItem, lang, "04000000")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := it.Info["price"]; ok {
		t.Errorf("覆盖后仍留着 price，info 没整包替换: %+v", it.Info)
	}
	if it.Info["slotMax"] != "200" {
		t.Errorf("值没被覆盖: %+v", it.Info)
	}
	// 清掉该文件不再贡献的 ID。
	n, err := d.ClearSide(lang, []IDRef{{ID: "04000001"}, {ID: "04000000"}}, SideInfo, "Item.wz/Etc/0430.img.xml", true)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("只该清掉 04000001，实际 %d", n)
	}
	one, _, err := d.Get(KindItem, lang, "04000001")
	if err != nil {
		t.Fatal(err)
	}
	if one.HasInfo || len(one.Info) != 0 {
		t.Errorf("失效贡献没清掉: %+v", one)
	}
}

func TestClearSideKeepsIDsOwnedByOtherFiles(t *testing.T) {
	d := openTestDB(t)
	at := time.Now().Truncate(time.Second)
	if _, err := d.PutNames(lang, []NameArg{{ID: "04000000", Name: "蓝色蜗牛壳", Category: "Etc"}}); err != nil {
		t.Fatal(err)
	}
	// 两个名称侧文件都贡献了这个 ID（真实库里 Eqp 分组与总表就可能重叠）。
	saveFingerprints(t, d, at,
		FileRec{Path: "String.wz/Item.img.xml", Size: 10, IDs: []string{"04000000"}},
		FileRec{Path: "String.wz/Eqp.img.xml", Size: 20, IDs: []string{"04000000"}},
	)
	n, err := d.ClearSide(lang, []IDRef{{ID: "04000000"}}, SideName, "String.wz/Item.img.xml", true)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("另一个文件仍认领该 ID，不该清除，实际清了 %d", n)
	}
	it, _, err := d.Get(KindItem, lang, "04000000")
	if err != nil {
		t.Fatal(err)
	}
	if it.Name != "蓝色蜗牛壳" {
		t.Errorf("名称被误清: %+v", it)
	}
}

func TestEditedRowsSurviveReload(t *testing.T) {
	d := openTestDB(t)
	if _, err := d.PutNames(lang, []NameArg{{ID: "04000000", Name: "蓝色蜗牛壳", Category: "Etc"}}); err != nil {
		t.Fatal(err)
	}
	ok, err := d.UpdateItem(KindItem, lang, "04000000", ItemEdit{Name: "手工改名", Descr: "备注", Category: "Etc",
		Info: map[string]string{"slotMax": "10"}, Edited: true})
	if err != nil || !ok {
		t.Fatalf("UpdateItem: ok=%v err=%v", ok, err)
	}
	if n, err := d.EditedCount(KindItem, lang); err != nil || n != 1 {
		t.Fatalf("EditedCount: n=%d err=%v", n, err)
	}
	// skipEdited=true 时强制覆盖要跳过它。
	if n, err := d.PutNamesForce(lang, []NameArg{{ID: "04000000", Name: "XML 里的名字"}}, true); err != nil || n != 0 {
		t.Fatalf("应跳过手工行: n=%d err=%v", n, err)
	}
	it, _, err := d.Get(KindItem, lang, "04000000")
	if err != nil {
		t.Fatal(err)
	}
	if it.Name != "手工改名" || !it.Edited || it.Info["slotMax"] != "10" || !it.HasInfo {
		t.Errorf("手工行被改动: %+v", it)
	}
	// 明确要求覆盖时才生效。
	if n, err := d.PutNamesForce(lang, []NameArg{{ID: "04000000", Name: "XML 里的名字"}}, false); err != nil || n != 1 {
		t.Fatalf("覆盖手工标记应生效: n=%d err=%v", n, err)
	}
	if it, _, _ = d.Get(KindItem, lang, "04000000"); it.Name != "XML 里的名字" {
		t.Errorf("没覆盖上: %+v", it)
	}
	// 明确覆盖后，这行就不再算手工修改。
	if it.Edited {
		t.Errorf("覆盖后仍带手工标记: %+v", it)
	}
	if n, err := d.EditedCount(KindItem, lang); err != nil || n != 0 {
		t.Errorf("覆盖后手工计数应归零: n=%d err=%v", n, err)
	}
	// 不存在的 ID 不该凭空建行。
	if ok, err := d.UpdateItem(KindItem, lang, "09999999", ItemEdit{Name: "x"}); err != nil || ok {
		t.Errorf("UpdateItem 对不存在的 ID 应返回 false: ok=%v err=%v", ok, err)
	}
}

func TestDeleteFilesClearsOnlyExclusiveContributions(t *testing.T) {
	d := openTestDB(t)
	at := time.Now().Truncate(time.Second)
	if _, err := d.PutNames(lang, []NameArg{
		{ID: "04000000", Name: "蓝色蜗牛壳"}, {ID: "02000000", Name: "红色药水"}}); err != nil {
		t.Fatal(err)
	}
	saveFingerprints(t, d, at,
		FileRec{Path: "String.wz/Item.img.xml", Size: 10, IDs: []string{"04000000", "02000000"}},
		FileRec{Path: "String.wz/Eqp.img.xml", Size: 20, IDs: []string{"02000000"}},
	)
	files, cleared, err := d.DeleteFiles(lang, []string{"String.wz/Item.img.xml"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if files != 1 || cleared != 1 {
		t.Errorf("应删 1 个文件、清 1 条独占贡献，实际 files=%d cleared=%d", files, cleared)
	}
	if _, ok, _ := d.Get(KindItem, lang, "04000000"); ok {
		if it, _, _ := d.Get(KindItem, lang, "04000000"); it.HasName {
			t.Errorf("04000000 的名称没清: %+v", it)
		}
	}
	// 02000000 仍被 Eqp 认领，名称必须留着。
	if it, ok, _ := d.Get(KindItem, lang, "02000000"); !ok || it.Name != "红色药水" {
		t.Errorf("共享 ID 被误清: %+v", it)
	}
	if n, err := d.FileIDs(lang, "String.wz/Item.img.xml"); err != nil || n != nil {
		t.Errorf("文件记录应已删除: %v err=%v", n, err)
	}
}

func TestOpenAddsColumnsToMidVersionDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mid.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// 上一版结构：已有 lang 分域，但 item 无 edited、wz_file 无 ids。
	for _, stmt := range []string{
		`CREATE TABLE item (lang TEXT NOT NULL, id TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', descr TEXT NOT NULL DEFAULT '', category TEXT NOT NULL DEFAULT '', info TEXT NOT NULL DEFAULT '{}', has_name INTEGER NOT NULL DEFAULT 0, has_info INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL, PRIMARY KEY (lang, id))`,
		`CREATE TABLE wz_file (lang TEXT NOT NULL, path TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL, status TEXT NOT NULL, err TEXT NOT NULL DEFAULT '', repaired INTEGER NOT NULL DEFAULT 0, rows INTEGER NOT NULL DEFAULT 0, scanned_at INTEGER NOT NULL, PRIMARY KEY (lang, path))`,
		`INSERT INTO item(lang,id,name,has_name,updated_at) VALUES('zh-CN','04000000','蓝色蜗牛壳',1,1700000000)`,
		`INSERT INTO wz_file(lang,path,size,mtime,status,scanned_at) VALUES('zh-CN','String.wz/Item.img.xml',10,20,'ok',30)`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("建中间版库失败: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := Open(path)
	if err != nil {
		t.Fatalf("补列迁移失败: %v", err)
	}
	defer d.Close()
	it, ok, err := d.Get(KindItem, "zh-CN", "04000000")
	if err != nil || !ok || it.Edited {
		t.Fatalf("旧行读回错: ok=%v err=%v %+v", ok, err, it)
	}
	if ok, err := d.HasProvenance("zh-CN"); err != nil || ok {
		t.Errorf("老记录没有溯源，HasProvenance 应为 false: %v err=%v", ok, err)
	}
	// 新写入要带上溯源列。
	saveFingerprints(t, d, time.Now().Truncate(time.Second),
		FileRec{Path: "Item.wz/Etc/0430.img.xml", Size: 5, IDs: []string{"04000000"}})
	if ok, err := d.HasProvenance("zh-CN"); err != nil || !ok {
		t.Errorf("写入后应检测到溯源: %v err=%v", ok, err)
	}
}

func TestListFilesAndRuns(t *testing.T) {
	d := openTestDB(t)
	at := time.Now().Truncate(time.Second)
	saveFingerprints(t, d, at,
		FileRec{Path: "Character.wz/Cap/01000000.img.xml", Size: 10, IDs: []string{"01000000", "01000001"}},
		FileRec{Path: "Item.wz/Etc/0430.img.xml", Size: 20, Status: "failed", Err: "解析炸了"},
	)
	rows, total, err := d.ListFiles(lang, "Cap/", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].IDs != 2 {
		t.Errorf("文件列表错: total=%d %+v", total, rows)
	}
	// 路径里的 % 必须按字面量匹配，不能当通配符。
	if _, total, err := d.ListFiles(lang, "04%30", "", 10, 0); err != nil || total != 0 {
		t.Errorf("LIKE 通配符没转义: total=%d err=%v", total, err)
	}
	if _, total, err := d.ListFiles(lang, "", "failed", 10, 0); err != nil || total != 1 {
		t.Errorf("按状态过滤错: total=%d err=%v", total, err)
	}
	id, err := d.StartRun(lang, 3, "reload:2 String.wz")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.FinishRun(id, 2, 1); err != nil {
		t.Fatal(err)
	}
	runs, err := d.ListRuns(lang, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("历史读取错: len=%d err=%v", len(runs), err)
	}
	r := runs[0]
	if r.Parsed != 2 || r.Failed != 1 || r.Total != 3 || !strings.HasPrefix(r.Note, "reload:") {
		t.Errorf("历史内容错: %+v", r)
	}
	if r.StartedAt.IsZero() || r.FinishedAt.Before(r.StartedAt) {
		t.Errorf("时间戳没还原: %+v", r)
	}
}
