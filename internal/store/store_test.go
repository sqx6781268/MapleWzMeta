package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const lang = "zh-CN"

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestPendingOnlyReturnsChanged(t *testing.T) {
	d := openTestDB(t)
	now := time.Now().Truncate(time.Second)
	recs := []FileRec{
		{Path: "String.wz/Etc.img.xml", Size: 100, ModTime: now},
		{Path: "Item.wz/Etc/0430.img.xml", Size: 200, ModTime: now},
	}
	if err := d.SaveFiles(lang, recs); err != nil {
		t.Fatalf("SaveFiles: %v", err)
	}

	next := []FileRec{
		{Path: "String.wz/Etc.img.xml", Size: 100, ModTime: now},            // 未变
		{Path: "Item.wz/Etc/0430.img.xml", Size: 201, ModTime: now},         // 大小变了
		{Path: "Character.wz/Cap/01000000.img.xml", Size: 50, ModTime: now}, // 新文件
	}
	got, err := d.Pending(lang, next)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应只有 2 个待处理，实际 %d: %+v", len(got), got)
	}
	if got[0].Path != "Item.wz/Etc/0430.img.xml" || got[1].Path != "Character.wz/Cap/01000000.img.xml" {
		t.Errorf("待处理清单不对: %s / %s", got[0].Path, got[1].Path)
	}

	// 另一语言域没有任何记录，三个文件都要重解析。
	other, err := d.Pending("en", next)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 3 {
		t.Errorf("语言域未隔离，en 下应全部待处理，实际 %d", len(other))
	}
}

func TestSidesDoNotOverwriteEachOther(t *testing.T) {
	d := openTestDB(t)

	if n, err := d.PutNames(lang, []NameArg{{ID: "01002000", Name: "骑士头盔", Descr: "描述", Category: "Cap"}}); err != nil || n != 1 {
		t.Fatalf("PutNames: n=%d err=%v", n, err)
	}
	if n, err := d.PutInfos(lang, []InfoArg{{ID: "01002000", Category: "Cap", Info: map[string]string{"reqLevel": "15"}}}); err != nil || n != 1 {
		t.Fatalf("PutInfos: n=%d err=%v", n, err)
	}
	// 第二次只写属性，不应清空名称；且 info 要按 key 合并。
	if _, err := d.PutInfos(lang, []InfoArg{{ID: "01002000", Category: "Cap", Info: map[string]string{"islot": "Cp"}}}); err != nil {
		t.Fatalf("PutInfos 合并: %v", err)
	}
	// 空名称不应覆盖已有名称。
	if _, err := d.PutNames(lang, []NameArg{{ID: "01002000", Name: "  ", Category: "Cap"}}); err != nil {
		t.Fatalf("PutNames 空名: %v", err)
	}
	// 另一语言写同名 ID 的英文名称，不能污染 zh-CN。
	if _, err := d.PutNames("en", []NameArg{{ID: "01002000", Name: "Knight Helm", Category: "Cap"}}); err != nil {
		t.Fatalf("PutNames en: %v", err)
	}

	it, ok, err := d.Get(KindItem, lang, "01002000")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if it.Name != "骑士头盔" || it.Descr != "描述" {
		t.Errorf("名称侧被覆盖: %+v", it)
	}
	if it.Info["reqLevel"] != "15" || it.Info["islot"] != "Cp" {
		t.Errorf("info 未合并: %+v", it.Info)
	}
	if !it.HasName || !it.HasInfo {
		t.Errorf("has_name/has_info 标记错: %+v", it)
	}
	en, ok, err := d.Get(KindItem, "en", "01002000")
	if err != nil || !ok {
		t.Fatalf("Get en: ok=%v err=%v", ok, err)
	}
	if en.Name != "Knight Helm" || en.HasInfo {
		t.Errorf("en 域被 zh-CN 污染: %+v", en)
	}
}

func TestSearchFilters(t *testing.T) {
	d := openTestDB(t)
	if _, err := d.PutNames(lang, []NameArg{
		{ID: "01002000", Name: "骑士头盔", Category: "Cap"},
		{ID: "02000000", Name: "红色药水", Category: "Consume"},
		{ID: "04000000", Name: "蜗牛壳", Category: "Etc"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PutInfos(lang, []InfoArg{
		{ID: "01002000", Category: "Cap", Info: map[string]string{"cash": "1", "icon.width": "30", "price": "100"}},
		{ID: "02000000", Category: "Consume", Info: map[string]string{"cash": "0", "price": "50"}},
		{ID: "01002999", Category: "Cap", Info: map[string]string{"reqLevel": "20"}},
	}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		q    Query
		want []string
	}{
		{"名称模糊", Query{Name: "药水"}, []string{"02000000"}},
		{"分类", Query{Category: "Cap"}, []string{"01002000", "01002999"}},
		{"ID前缀", Query{IDPrefix: "02"}, []string{"02000000"}},
		// 库里存 8 位补零 ID，用户手输的是原始 7 位号段，两种前缀都要命中。
		{"ID前缀补零", Query{IDPrefix: "2000"}, []string{"02000000"}},
		{"ID前缀补零2", Query{IDPrefix: "4000"}, []string{"04000000"}},
		{"ID前缀八位", Query{IDPrefix: "04000000"}, []string{"04000000"}},
		{"属性等值", Query{Wants: []string{"cash=1"}}, []string{"01002000"}},
		{"含点键", Query{Wants: []string{"icon.width=30"}}, []string{"01002000"}},
		{"属性数值下界", Query{GTE: map[string]int64{"price": 60}}, []string{"01002000"}},
		// 上界必须排除没有该键的物品：否则缺 price 的会被当成 0 而命中 price<=60。
		{"属性数值上界", Query{LTE: map[string]int64{"price": 60}}, []string{"02000000"}},
		{"属性键存在", Query{Has: []string{"reqLevel"}}, []string{"01002999"}},
		{"属性值包含", Query{Like: []string{"price=10"}}, []string{"01002000"}},
		{"只要无名", Query{HasName: boolp(false)}, []string{"01002999"}},
		{"只要无属性", Query{HasInfo: boolp(false)}, []string{"04000000"}},
		{"只要齐全", Query{HasName: boolp(true), HasInfo: boolp(true)}, []string{"01002000", "02000000"}},
	}
	for _, c := range cases {
		q := c.q
		q.Lang = lang
		rows, total, err := d.Search(q)
		if err != nil {
			t.Errorf("%s: Search 报错 %v", c.name, err)
			continue
		}
		if total != len(c.want) {
			t.Errorf("%s: 命中 %d，期望 %d", c.name, total, len(c.want))
			continue
		}
		for i, r := range rows {
			if r.ID != c.want[i] {
				t.Errorf("%s: 第 %d 条是 %s，期望 %s", c.name, i, r.ID, c.want[i])
			}
			if r.Lang != lang {
				t.Errorf("%s: 返回了别的语言域 %s", c.name, r.Lang)
			}
		}
	}
}

func TestStatsAndCategories(t *testing.T) {
	d := openTestDB(t)
	if _, err := d.PutNames(lang, []NameArg{
		{ID: "01002000", Name: "骑士头盔", Category: "Cap"},
		{ID: "01002001", Name: "蓝帽", Category: "Cap"},
		{ID: "02000000", Name: "红药", Category: "Consume"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PutInfos(lang, []InfoArg{{ID: "01002000", Category: "Cap", Info: map[string]string{"cash": "1"}}}); err != nil {
		t.Fatal(err)
	}
	runID, err := d.StartRun(lang, 3, "测试")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := d.SaveFiles(lang, []FileRec{
		{Path: "a", Size: 1, ModTime: time.Now()},
		{Path: "b", Size: 1, ModTime: time.Now(), Status: "failed", Err: "炸了"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishRun(runID, 1, 1); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	st, err := d.Stats(KindItem, lang)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Lang != lang {
		t.Errorf("Stats 未带语言: %+v", st)
	}
	if st.Items != 3 || st.Named != 3 || st.WithInfo != 1 || st.Complete != 1 {
		t.Errorf("Stats 计数错: %+v", st)
	}
	if st.Files != 2 || st.Failed != 1 {
		t.Errorf("文件计数错: %+v", st)
	}
	if st.LastRunUnix == 0 {
		t.Errorf("LastRunUnix 未记录")
	}
	if len(st.Categories) == 0 || st.Categories[0].Name != "Cap" || st.Categories[0].Count != 2 {
		t.Errorf("分类统计错: %+v", st.Categories)
	}

	cats, err := d.Categories(KindItem, lang)
	if err != nil {
		t.Fatalf("Categories: %v", err)
	}
	if len(cats) != 2 {
		t.Errorf("分类数期望 2，实际 %d", len(cats))
	}
	// 空语言域应什么都不返回。
	empty, err := d.Stats(KindItem, "en")
	if err != nil || empty.Items != 0 {
		t.Errorf("en 域应为空: %+v err=%v", empty, err)
	}
	languages, err := d.Langs(KindItem)
	if err != nil {
		t.Fatal(err)
	}
	if len(languages) != 1 || languages[0].Lang != lang || languages[0].Items != 3 {
		t.Errorf("Langs 汇总错: %+v", languages)
	}
}

func TestDeleteMissingAndRunRoundTrip(t *testing.T) {
	d := openTestDB(t)
	if err := d.SaveFiles(lang, []FileRec{
		{Path: "keep", Size: 1, ModTime: time.Now()},
		{Path: "gone", Size: 1, ModTime: time.Now()},
	}); err != nil {
		t.Fatal(err)
	}
	n, err := d.DeleteMissing(lang, []string{"keep"})
	if err != nil {
		t.Fatalf("DeleteMissing: %v", err)
	}
	if n != 1 {
		t.Errorf("应删 1 条，实际 %d", n)
	}
	p, err := d.Pending(lang, []FileRec{{Path: "keep", Size: 1, ModTime: time.Now()}})
	if err != nil || len(p) != 0 {
		t.Errorf("保留记录应仍在库中: %v err=%v", p, err)
	}
}

func TestOpenMigratesLegacySchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE wz_file (path TEXT PRIMARY KEY, size INTEGER NOT NULL, mtime INTEGER NOT NULL, status TEXT NOT NULL, err TEXT NOT NULL DEFAULT '', repaired INTEGER NOT NULL DEFAULT 0, rows INTEGER NOT NULL DEFAULT 0, scanned_at INTEGER NOT NULL)`,
		`CREATE TABLE item (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', descr TEXT NOT NULL DEFAULT '', category TEXT NOT NULL DEFAULT '', info TEXT NOT NULL DEFAULT '{}', has_name INTEGER NOT NULL DEFAULT 0, has_info INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL)`,
		`CREATE INDEX idx_item_name ON item(name)`,
		`CREATE INDEX idx_item_cat ON item(category)`,
		`CREATE TABLE scan_run (id INTEGER PRIMARY KEY AUTOINCREMENT, started_at INTEGER NOT NULL, finished_at INTEGER, total INTEGER NOT NULL DEFAULT 0, parsed INTEGER NOT NULL DEFAULT 0, failed INTEGER NOT NULL DEFAULT 0, note TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO item(id,name,descr,category,info,has_name,has_info,updated_at) VALUES('04000000','蓝色蜗牛壳','描述','Etc','{"slotMax":"100"}',1,1,1700000000)`,
		`INSERT INTO wz_file(path,size,mtime,status,scanned_at) VALUES('Item.wz/Etc/0430.img.xml',10,20,'ok',30)`,
		`INSERT INTO scan_run(started_at,total) VALUES(1700000000,1)`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("建旧库失败: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open 迁移失败: %v", err)
	}
	defer d.Close()

	it, ok, err := d.Get(KindItem, LegacyLang, "04000000")
	if err != nil || !ok {
		t.Fatalf("迁移后查不到旧数据: ok=%v err=%v", ok, err)
	}
	if it.Name != "蓝色蜗牛壳" || it.Info["slotMax"] != "100" || !it.HasName || !it.HasInfo {
		t.Errorf("迁移后内容错: %+v", it)
	}
	// 旧指纹必须继续参与增量判断。
	p, err := d.Pending(LegacyLang, []FileRec{{Path: "Item.wz/Etc/0430.img.xml", Size: 10, ModTime: time.Unix(20, 0)}})
	if err != nil || len(p) != 0 {
		t.Errorf("旧指纹未生效: %v err=%v", p, err)
	}
	st, err := d.Stats(KindItem, LegacyLang)
	if err != nil || st.Items != 1 || st.Files != 1 {
		t.Errorf("迁移后统计错: %+v err=%v", st, err)
	}
	// 迁移后再打开一次不应重复迁移或报错。
	again, err := Open(path)
	if err != nil {
		t.Fatalf("二次 Open 失败: %v", err)
	}
	defer again.Close()
	if _, ok, err := again.Get(KindItem, LegacyLang, "04000000"); !ok || err != nil {
		t.Errorf("二次打开数据丢失: ok=%v err=%v", ok, err)
	}
}

func TestUncategorizedAndAttrKeys(t *testing.T) {
	d := openTestDB(t)
	if _, err := d.PutNames(lang, []NameArg{
		{ID: "09999000", Name: "无归类物品"},
		{ID: "01002000", Name: "骑士头盔", Category: "Cap"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PutInfos(lang, []InfoArg{
		{ID: "09999000", Info: map[string]string{"cash": "1", "price": "100"}},
		{ID: "01002000", Category: "Cap", Info: map[string]string{"cash": "1"}},
	}); err != nil {
		t.Fatal(err)
	}

	// 分类为空时统计里显示成 NoCategoryName，检索要能反向命中。
	cats, err := d.Categories(KindItem, lang)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range cats {
		if c.Name == NoCategoryName {
			found = true
		}
	}
	if !found {
		t.Errorf("分类统计未出现 %q: %+v", NoCategoryName, cats)
	}
	q := Query{Lang: lang, Category: NoCategoryName}
	rows, total, err := d.Search(q)
	if err != nil || total != 1 || rows[0].ID != "09999000" {
		t.Errorf("按未分类检索错: total=%d rows=%+v err=%v", total, rows, err)
	}

	// 属性键频次统计按覆盖面降序，且只统计本语言域。
	ks, err := d.AttrKeys(KindItem, lang, 0)
	if err != nil {
		t.Fatalf("AttrKeys: %v", err)
	}
	if len(ks) != 2 || ks[0].Key != "cash" || ks[0].Count != 2 {
		t.Errorf("属性键统计错: %+v", ks)
	}
	if ks[1].Key != "price" || ks[1].Count != 1 {
		t.Errorf("属性键排序错: %+v", ks)
	}
	lim, err := d.AttrKeys(KindItem, lang, 1)
	if err != nil || len(lim) != 1 {
		t.Errorf("AttrKeys limit 未生效: %+v err=%v", lim, err)
	}
	other, err := d.AttrKeys(KindItem, "en", 0)
	if err != nil || len(other) != 0 {
		t.Errorf("en 域不该有属性键: %+v", other)
	}
}

func boolp(b bool) *bool { return &b }
