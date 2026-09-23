package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openKindDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "kind.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// 三个实体域共用一个 8 位 ID 空间：物品 01110100 是戒指，怪物 01110100 是绿蘑菇。
// 分表的意义就在于两侧互不覆盖，因此这里同时写同 ID 的物品属性与怪物名称。
func TestKindsAreIsolatedTables(t *testing.T) {
	d := openKindDB(t)
	const id = "01110100"
	if _, err := d.PutNames(lang, []NameArg{{Kind: KindMob, ID: id, Name: "绿蘑菇"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PutInfos(lang, []InfoArg{{Kind: KindItem, ID: id, Category: "Ring",
		Info: map[string]string{"slot": "1"}}}); err != nil {
		t.Fatal(err)
	}

	mob, ok, err := d.Get(KindMob, lang, id)
	if err != nil || !ok {
		t.Fatalf("怪物域应能读到 %s: ok=%v err=%v", id, ok, err)
	}
	if mob.Name != "绿蘑菇" || mob.HasInfo {
		t.Errorf("怪物行不该带物品属性: %+v", mob)
	}
	it, ok, err := d.Get(KindItem, lang, id)
	if err != nil || !ok {
		t.Fatalf("物品域应能读到 %s: ok=%v err=%v", id, ok, err)
	}
	if it.Name != "" || it.Category != "Ring" {
		t.Errorf("物品行被怪物名称污染: %+v", it)
	}
	for _, c := range []struct {
		kind Kind
		want int
	}{{KindItem, 1}, {KindMob, 1}, {KindNPC, 0}} {
		rows, total, err := d.Search(Query{Kind: c.kind, Lang: lang})
		if err != nil {
			t.Fatal(err)
		}
		if total != c.want || len(rows) != c.want {
			t.Errorf("%s 域应 %d 条，实际 %d/%d", c.kind, c.want, total, len(rows))
		}
	}
	st, err := d.Stats(KindMob, lang)
	if err != nil {
		t.Fatal(err)
	}
	if st.Items != 1 || st.Named != 1 || st.WithInfo != 0 {
		t.Errorf("怪物域概况错: %+v", st)
	}
}

// 清理某文件的失效贡献只按"域 + ID"判归属：怪物侧的 ID 只有怪物文件认领，
// 同 ID 的物品行不该被牵连，反之怪物的行也不会因为物品在认领就清不掉。
func TestClearSideOnlyTouchesOwnKind(t *testing.T) {
	d := openKindDB(t)
	const id = "01110100"
	if _, err := d.PutNames(lang, []NameArg{
		{Kind: KindMob, ID: id, Name: "绿蘑菇"},
		{Kind: KindItem, ID: id, Name: "力量戒指"},
	}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Truncate(time.Second)
	if err := d.SaveFiles(lang, []FileRec{
		{Path: "String.wz/Mob.img.xml", Size: 10, ModTime: at, Status: "ok", Kind: KindMob, IDs: []string{id}},
		{Path: "String.wz/Eqp.img.xml", Size: 20, ModTime: at, Status: "ok", Kind: KindItem, IDs: []string{id}},
	}); err != nil {
		t.Fatal(err)
	}
	refs, err := d.FileIDs(lang, "String.wz/Mob.img.xml")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Kind != KindMob {
		t.Fatalf("溯源清单错: %+v", refs)
	}
	n, err := d.ClearSide(lang, refs, SideName, "String.wz/Mob.img.xml", false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("怪物侧失效贡献应清掉 1 行，实际 %d", n)
	}
	if _, _, err := d.DeleteFiles(lang, []string{"String.wz/Eqp.img.xml"}, false); err != nil {
		t.Fatal(err)
	}
	mob, ok, err := d.Get(KindMob, lang, id)
	if err != nil || !ok {
		t.Fatalf("怪物行不该被删表: ok=%v err=%v", ok, err)
	}
	if mob.Name != "" || mob.HasName {
		t.Errorf("怪物名称应已被清（那是怪物文件唯一的贡献）: %+v", mob)
	}
	it, _, err := d.Get(KindItem, lang, id)
	if err != nil {
		t.Fatal(err)
	}
	if it.Name != "" || it.HasName {
		t.Errorf("物品侧失效贡献没清掉: %+v", it)
	}
}

// 来源反查必须按域区分，否则查一个物品会把 Monster 的 XML 列成来源。
// 同时验证早于该格式的裸 ID 溯源按物品解释。
func TestSourcesOfSeparatesKinds(t *testing.T) {
	d := openKindDB(t)
	const id = "01110100"
	at := time.Now().Truncate(time.Second)
	if err := d.SaveFiles(lang, []FileRec{
		{Path: "String.wz/Eqp.img.xml", Size: 1, ModTime: at, Status: "ok", Kind: KindItem, IDs: []string{id}},
		{Path: "Mob.wz/1110100.img.xml", Size: 1, ModTime: at, Status: "ok", Kind: KindMob, IDs: []string{id}},
		{Path: "Item.wz/Ring/0110.img.xml", Size: 1, ModTime: at, Status: "ok", IDs: []string{id}},
	}); err != nil {
		t.Fatal(err)
	}
	// 手工写一条没有 kind 前缀的旧格式溯源。
	if _, err := d.sqlDB.Exec(`UPDATE wz_file SET ids = ? WHERE path = ?`,
		`["`+id+`"]`, "Item.wz/Ring/0110.img.xml"); err != nil {
		t.Fatal(err)
	}

	mob, err := d.SourcesOf(KindMob, lang, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(mob) != 1 || mob[0] != "Mob.wz/1110100.img.xml" {
		t.Errorf("怪物域来源错: %v", mob)
	}
	items, err := d.SourcesOf(KindItem, lang, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("物品域应有 Eqp 与旧格式 Ring 两条，实际 %v", items)
	}
	for _, p := range items {
		if p == "Mob.wz/1110100.img.xml" {
			t.Errorf("物品域混进了怪物来源: %v", items)
		}
	}
}

// StaleIDs 比对的是"域 + ID"，同 ID 换个域不该被当成还在贡献。
func TestStaleIDsIsKindScoped(t *testing.T) {
	prev := []IDRef{{Kind: KindMob, ID: "00000021"}, {Kind: KindItem, ID: "00000021"}}
	got := StaleIDs(prev, KindMob, []string{"00000021"})
	if len(got) != 1 || got[0].Kind != KindItem {
		t.Errorf("只应留下物品域那条，实际 %+v", got)
	}
}

// 手工标记按域存：改过物品不该让同 ID 的怪物行也被保护。
func TestEditedProtectionIsPerKind(t *testing.T) {
	d := openKindDB(t)
	const id = "01110100"
	if _, err := d.PutNames(lang, []NameArg{
		{Kind: KindItem, ID: id, Name: "力量戒指"},
		{Kind: KindMob, ID: id, Name: "绿蘑菇"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpdateItem(KindItem, lang, id, ItemEdit{Name: "手改戒指", Edited: true}); err != nil {
		t.Fatal(err)
	}
	if n, err := d.EditedCount(KindItem, lang); err != nil || n != 1 {
		t.Errorf("物品域手工数 = %d err=%v", n, err)
	}
	if n, err := d.EditedCount(KindMob, lang); err != nil || n != 0 {
		t.Errorf("怪物域不该有手工行，实际 %d err=%v", n, err)
	}
	n, err := d.PutNamesForce(lang, []NameArg{
		{Kind: KindItem, ID: id, Name: "戒指覆盖掉了"},
		{Kind: KindMob, ID: id, Name: "怪物蘑菇"},
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("应只写入怪物域 1 条，实际 %d", n)
	}
	if it, _, _ := d.Get(KindItem, lang, id); it.Name != "手改戒指" {
		t.Errorf("手工物品行被覆盖: %+v", it)
	}
	if mob, _, _ := d.Get(KindMob, lang, id); mob.Name != "怪物蘑菇" {
		t.Errorf("怪物域没被写入: %+v", mob)
	}
}

// 显式覆盖成功后不再算手工行，否则同一行会永久挡住后续重载。
func TestForceClearsEditedAcrossKinds(t *testing.T) {
	d := openKindDB(t)
	if _, err := d.PutNames(lang, []NameArg{{Kind: KindNPC, ID: "00000700", Name: "老名"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpdateItem(KindNPC, lang, "00000700", ItemEdit{Name: "手工名", Edited: true}); err != nil {
		t.Fatal(err)
	}
	// skipEdited=false 即管理页勾了"同时覆盖手工修改"。
	if _, err := d.PutNamesForce(lang, []NameArg{{Kind: KindNPC, ID: "00000700", Name: "XML 名"}}, false); err != nil {
		t.Fatal(err)
	}
	row, ok, err := d.Get(KindNPC, lang, "00000700")
	if err != nil || !ok {
		t.Fatalf("回读失败: ok=%v err=%v", ok, err)
	}
	if row.Name != "XML 名" || row.Edited {
		t.Errorf("覆盖后应清掉手工标记: %+v", row)
	}
}

// 检索里的 ID 一律按 8 位补零解释：怪物原生是 7 位，手输 1110100 也要命中。
func TestSearchNormalizesIDForAllKinds(t *testing.T) {
	d := openKindDB(t)
	if _, err := d.PutNames(lang, []NameArg{
		{Kind: KindMob, ID: "01110100", Name: "绿蘑菇"},
		{Kind: KindMob, ID: "00000021", Name: "僵尸蘑菇"},
	}); err != nil {
		t.Fatal(err)
	}
	rows, total, err := d.Search(Query{Kind: KindMob, Lang: lang, ID: "1110100"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || rows[0].Name != "绿蘑菇" {
		t.Errorf("7 位 ID 应补零命中，实际 %d 条 %+v", total, rows)
	}
	// 前缀检索按"原样"和"补一位零"两种写法试，怪物原生 7 位号因此能直接命中。
	_, total, err = d.Search(Query{Kind: KindMob, Lang: lang, IDPrefix: "1110100"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("前缀 1110100 应命中 01110100，实际 %d", total)
	}
}

// 详情页要分开显示"名称来自哪张 String 表""属性来自哪个包文件"，
// 所以来源清单按贡献侧拆开，而不是合成一个列表。
func TestSourcesBySideSplitsNameAndInfo(t *testing.T) {
	d := openKindDB(t)
	at := time.Now().Truncate(time.Second)
	if err := d.SaveFiles(lang, []FileRec{
		{Path: "String.wz/Eqp.img.xml", Size: 1, ModTime: at, Status: "ok", Kind: KindItem, IDs: []string{"01000001"}},
		{Path: "Character.wz/Cap/01000001.img.xml", Size: 1, ModTime: at, Status: "ok", Kind: KindItem, IDs: []string{"01000001"}},
		{Path: "Mob.wz/1000001.img.xml", Size: 1, ModTime: at, Status: "ok", Kind: KindMob, IDs: []string{"01000001"}},
	}); err != nil {
		t.Fatal(err)
	}
	src, err := d.SourcesBySide(KindItem, lang, "1000001") // 7 位写法也要能查到
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Name) != 1 || src.Name[0] != "String.wz/Eqp.img.xml" {
		t.Errorf("名称侧来源错: %v", src.Name)
	}
	if len(src.Info) != 1 || src.Info[0] != "Character.wz/Cap/01000001.img.xml" {
		t.Errorf("属性侧来源错: %v", src.Info)
	}
	mob, err := d.SourcesBySide(KindMob, lang, "01000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(mob.Name) != 0 || len(mob.Info) != 1 || mob.Info[0] != "Mob.wz/1000001.img.xml" {
		t.Errorf("怪物域来源被物品域串了: %+v", mob)
	}
	// SourcesOf 是两侧的合并清单，管理页一键重解析要用。
	all, err := d.SourcesOf(KindItem, lang, "01000001")
	if err != nil || len(all) != 2 {
		t.Errorf("SourcesOf 应合并两条，实际 %v err=%v", all, err)
	}
}
