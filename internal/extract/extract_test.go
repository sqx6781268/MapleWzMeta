package extract_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sqx6781268/MapleWzMeta/internal/binder"
	"github.com/sqx6781268/MapleWzMeta/internal/extract"
	"github.com/sqx6781268/MapleWzMeta/internal/wzxml"
)

func wzRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("MAPLEWZMETA_WZ")
	if root == "" {
		root = filepath.Join("..", "..", "wz")
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("缺少 WZ 导出目录 %s: %v", root, err)
	}
	return root
}

func load(t *testing.T, rel string) *wzxml.Node {
	t.Helper()
	p := filepath.Join(wzRoot(t), filepath.FromSlash(rel))
	n, info, err := wzxml.DecodeFileRobust(p)
	if err != nil {
		t.Fatalf("解码 %s 失败: %v", rel, err)
	}
	if info.RepairedTags > 0 || info.NormalizedEncoding {
		t.Logf("%s 经过容错处理: %+v", rel, info)
	}
	return n
}

func TestNormalizeID(t *testing.T) {
	cases := map[string]string{
		"1010000":      "01010000",
		"01010000":     "01010000",
		"01010000.img": "01010000",
		" 400 ":        "00000400",
		"100100":       "00100100", // 怪物 ID 也会被补齐，调用方需按域区分
		"d0":           "d0",
		"":             "",
		"5010000":      "05010000",
		"china":        "china",
		"00002000.img": "00002000",
	}
	for in, want := range cases {
		if got := extract.NormalizeID(in); got != want {
			t.Errorf("NormalizeID(%q) = %q，期望 %q", in, got, want)
		}
	}
	if !extract.LooksLikeID(extract.KindItem, "", "1010000") ||
		extract.LooksLikeID(extract.KindItem, "", "101000") ||
		extract.LooksLikeID(extract.KindItem, "", "100100") ||
		extract.LooksLikeID(extract.KindItem, "", "abc12345") {
		t.Error("物品域位数判定不符：只接受 7 或 8 位纯数字")
	}
}

// String.wz 各文件层级不一致，抽取必须与层数无关。
func TestNamesFromAllStringLayouts(t *testing.T) {
	cases := []struct {
		file     string
		wantID   string
		wantName string
		category string
	}{
		{"String.wz/Cash.img.xml", "05010000", "太阳效果", ""},
		{"String.wz/Ins.img.xml", "03010000", "休闲椅", ""},
		{"String.wz/Etc.img.xml", "04000000", "蓝色蜗牛壳", "Etc"},
		{"String.wz/Eqp.img.xml", "01010000", "褐色落腮胡", "Accessory"},
	}
	for _, c := range cases {
		names := extract.Names(c.file, load(t, c.file))
		if len(names) == 0 {
			t.Fatalf("%s 未抽到任何名称", c.file)
		}
		var hit *extract.NameEntry
		for i := range names {
			if names[i].ID == c.wantID {
				hit = &names[i]
				break
			}
		}
		if hit == nil {
			t.Errorf("%s 未找到 ID %s（共 %d 条）", c.file, c.wantID, len(names))
			continue
		}
		if hit.Name != c.wantName {
			t.Errorf("%s/%s 名称 = %q，期望 %q", c.file, c.wantID, hit.Name, c.wantName)
		}
		if hit.Category != c.category {
			t.Errorf("%s/%s 分类 = %q，期望 %q", c.file, c.wantID, hit.Category, c.category)
		}
	}
}

func TestInfosFromItemAndCharacter(t *testing.T) {
	itemInfos := extract.Infos("Item.wz/Etc/0430.img.xml", load(t, "Item.wz/Etc/0430.img.xml"))
	if len(itemInfos) != 2 {
		t.Fatalf("Item.wz/Etc/0430 应得 2 条，实际 %d", len(itemInfos))
	}
	first := itemInfos[0]
	if first.ID != "04300000" || first.Source != "Item.wz" || first.Category != "Etc" {
		t.Errorf("首条记录异常: %+v", first)
	}
	if !first.HasInfoDir || first.Info["price"] != "1" || first.Info["slotMax"] != "1" {
		t.Errorf("info 抽取异常: %+v", first.Info)
	}
	// 画布类节点摊平成带前缀的键，避免与标量属性混在一起。
	if first.Info["icon.width"] != "26" || first.Info["icon.origin.x"] != "-3" {
		t.Errorf("canvas 摊平异常: %+v", first.Info)
	}

	charInfos := extract.Infos("Character.wz/Accessory/01010000.img.xml", load(t, "Character.wz/Accessory/01010000.img.xml"))
	if len(charInfos) != 1 {
		t.Fatalf("Character 单文件应得 1 条，实际 %d", len(charInfos))
	}
	ci := charInfos[0]
	if ci.ID != "01010000" || ci.Source != "Character.wz" || ci.Category != "Accessory" {
		t.Errorf("Character 记录异常: %+v", ci)
	}
	if ci.Info["islot"] != "Af" || ci.Info["vslot"] != "Af" || ci.Info["cash"] != "1" {
		t.Errorf("Character info 异常: %+v", ci.Info)
	}
	// info 之外的外观动画组要记入 Parts，用于判断数据完整度。
	if strings.Join(ci.Parts, ",") == "" || !strings.Contains(strings.Join(ci.Parts, ","), "blink") {
		t.Errorf("Parts 未记录外观组: %+v", ci.Parts)
	}
}

// 端到端：String.wz 的名称与 Character.wz 的属性按 ID 关联。
func TestBindEndToEnd(t *testing.T) {
	names := extract.Names("String.wz/Eqp.img.xml", load(t, "String.wz/Eqp.img.xml"))
	infos := extract.Infos("Character.wz/Accessory/01010000.img.xml", load(t, "Character.wz/Accessory/01010000.img.xml"))

	items := binder.Bind(names, infos)
	if len(items) == 0 {
		t.Fatal("关联结果为空")
	}
	var byID = map[string]binder.Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	got, ok := byID["01010000"]
	if !ok {
		t.Fatalf("未关联到 01010000，共 %d 条", len(items))
	}
	if !got.HasName || got.Name != "褐色落腮胡" {
		t.Errorf("名称关联失败: %+v", got)
	}
	if !got.HasInfo || got.Info["islot"] != "Af" {
		t.Errorf("属性关联失败: %+v", got.Info)
	}
	if got.Category != "Accessory" {
		t.Errorf("分类 = %q，期望 Accessory", got.Category)
	}
	if strings.Join(got.Sources, ",") != "character,name" {
		t.Errorf("Sources = %v", got.Sources)
	}
	// 只有名称没有属性的 ID 必须保留并标记，供缺失清单使用。
	if !byID["01010001"].HasName || byID["01010001"].HasInfo {
		t.Errorf("纯名称条目处理异常: %+v", byID["01010001"])
	}
	// 结果按 ID 升序，便于分页与 diff。
	for i := 1; i < len(items); i++ {
		if items[i-1].ID > items[i].ID {
			t.Fatalf("结果未按 ID 升序: %s > %s", items[i-1].ID, items[i].ID)
		}
	}
	st := binder.Summarize(items, 0)
	if st.Total != len(items) || st.Both != 1 {
		t.Errorf("Summarize 异常: %+v", st)
	}
	if st.NameOnly != len(items)-1 {
		t.Errorf("NameOnly = %d，期望 %d", st.NameOnly, len(items)-1)
	}
}

// 无名道具：有属性无名称时要进 NoNameIDs 清单。
func TestBindInfoOnly(t *testing.T) {
	infos := extract.Infos("Item.wz/Etc/0430.img.xml", load(t, "Item.wz/Etc/0430.img.xml"))
	items := binder.Bind(nil, infos)
	st := binder.Summarize(items, 10)
	if st.Total != 2 || st.InfoOnly != 2 || st.Both != 0 {
		t.Errorf("统计异常: %+v", st)
	}
	if len(st.NoNameIDs) != 2 || st.NoNameIDs[0] != "04300000" {
		t.Errorf("NoNameIDs = %v", st.NoNameIDs)
	}
}

// 名称侧白名单：只有登记过的表才产出行。
// 没登记的表（MonsterBook / Map 等）一条都不许出，否则就是 docs/附录 §4 记的跨实体名称污染；
// `Skill` 表登记给了技能域，改由 NameKindOf 校验它归到 skill 而非 item。
func TestNameWhitelistSkipsOtherDomains(t *testing.T) {
	for _, f := range []string{"String.wz/MonsterBook.img.xml", "String.wz/Map.img.xml"} {
		if got := extract.Names(f, load(t, f)); len(got) != 0 {
			t.Errorf("%s 不该产出任何条目，实际 %d 条", f, len(got))
		}
	}
	// Skill 表在册，但域必须是 skill——认成 item 就会写进物品表。
	if k, ok := extract.NameKindOf("String.wz/Skill.img.xml"); !ok || k != extract.KindSkill {
		t.Errorf("String.wz/Skill.img.xml 应归 skill 域，实际 %s/%v", k, ok)
	}
}

func TestKindOfAndNameKindOf(t *testing.T) {
	cases := []struct {
		rel  string
		want extract.Kind
	}{
		{"Mob.wz/0000021.img.xml", extract.KindMob},
		{"Npc.wz/0000700.img.xml", extract.KindNPC},
		{"Item.wz/Etc/0430.img.xml", extract.KindItem},
		{"Character.wz/Cap/01002000.img.xml", extract.KindItem},
		{"Skill.wz/000.img.xml", extract.KindSkill},
		{"Morph.wz/0001.img.xml", extract.KindMorph},
		{"Reactor.wz/0002000.img.xml", extract.KindReactor},
	}
	for _, c := range cases {
		if got := extract.KindOf(c.rel); got != c.want {
			t.Errorf("KindOf(%s) = %s，期望 %s", c.rel, got, c.want)
		}
	}
	nameCases := map[string]extract.Kind{
		"String.wz/Mob.img.xml":         extract.KindMob,
		"String.wz/Npc.img.xml":         extract.KindNPC,
		"String.wz/Eqp.img.xml":         extract.KindItem,
		"String.wz/Pet.img.xml":         extract.KindItem,
		"String.wz/Skill.img.xml":       extract.KindSkill,
		"String.wz/ToolTipHelp.img.xml": "",
		"Item.wz/Etc/0430.img.xml":      "",
	}
	for rel, want := range nameCases {
		k, ok := extract.NameKindOf(rel)
		if want == "" {
			if ok {
				t.Errorf("NameKindOf(%s) 不该在白名单里", rel)
			}
			continue
		}
		if !ok || k != want {
			t.Errorf("NameKindOf(%s) = %s/%v，期望 %s", rel, k, ok, want)
		}
	}
}

// Hair/Face 的名称用 5 位 ID 写在 Eqp 表里（实测 22,691 条），只对这张表放行；
// 怪物表的键有位数不等的写法（`21` 僵尸蘑菇、`1110100` 绿蘑菇），按数值归一到 8 位。
func TestNameIDLimitsPerDomain(t *testing.T) {
	eqp := extract.Names("String.wz/Eqp.img.xml", load(t, "String.wz/Eqp.img.xml"))
	if hit := findByNameID(eqp, "00020000"); hit == nil {
		t.Error("Eqp 的 5 位键 20000 应归一为 00020000 并抽到名称")
	} else if hit.Name != "酷-男脸(黑色)" {
		t.Errorf("00020000 名称 = %q", hit.Name)
	}
	// 位数门槛只对 Eqp 放宽：Consume 表里没有 5 位键，判据不受影响。
	if extract.LooksLikeID(extract.KindItem, "Consume", "20000") {
		t.Error("非 Eqp 表不该放行 5 位 ID")
	}

	mob := extract.Names("String.wz/Mob.img.xml", load(t, "String.wz/Mob.img.xml"))
	for _, c := range []struct{ id, name string }{
		{"01110100", "绿蘑菇"},
		{"00100100", "蜗牛"},
		{"00000021", "僵尸蘑菇"},
	} {
		hit := findByNameID(mob, c.id)
		if hit == nil {
			t.Errorf("怪物表未抽到 %s（共 %d 条）", c.id, len(mob))
			continue
		}
		if hit.Name != c.name {
			t.Errorf("%s 名称 = %q，期望 %q", c.id, hit.Name, c.name)
		}
	}
}

func findByNameID(list []extract.NameEntry, id string) *extract.NameEntry {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// "一个文件即一个实体"的形态要全部认出来：旧实现只对 Character.wz 开特判，
// 于是 Item.wz/Pet 的 461 个宠物道具、以及 Mob.wz/Npc.wz 整批属性漏抽。
func TestRootIDForSingleFileEntities(t *testing.T) {
	cases := map[string]string{
		"Character.wz/Accessory/01010000.img.xml": "01010000",
		"Item.wz/Pet/5000000.img.xml":             "05000000",
		"Mob.wz/0000021.img.xml":                  "00000021",
		"Npc.wz/0000700.img.xml":                  "00000700",
		// 分组文件根名同为数字但只有 4 位，不能当实体 ID。
		"Item.wz/Etc/0430.img.xml": "",
	}
	for rel, want := range cases {
		got := extract.RootID(rel, load(t, rel))
		if got != want {
			t.Errorf("RootID(%s) = %q，期望 %q", rel, got, want)
		}
	}

	mobInfos := extract.Infos("Mob.wz/0000021.img.xml", load(t, "Mob.wz/0000021.img.xml"))
	if len(mobInfos) != 1 {
		t.Fatalf("Mob 单文件应得 1 条，实际 %d", len(mobInfos))
	}
	mi := mobInfos[0]
	if mi.ID != "00000021" || mi.Kind != extract.KindMob || mi.Source != "Mob.wz" {
		t.Errorf("怪物记录异常: %+v", mi)
	}
	if mi.Info["level"] != "24" || mi.Info["maxHP"] != "443" {
		t.Errorf("怪物 info 抽取异常: %+v", mi.Info)
	}

	petInfos := extract.Infos("Item.wz/Pet/5000000.img.xml", load(t, "Item.wz/Pet/5000000.img.xml"))
	if len(petInfos) != 1 || petInfos[0].ID != "05000000" || petInfos[0].Kind != extract.KindItem {
		t.Fatalf("Item.wz/Pet 应抽到 1 条物品属性，实际 %+v", petInfos)
	}
}

// `Mob.wz/QuestCountGroup/` 下的文件同样是 7 位怪物号，但它是"怪物 → 任务计数"的查表，
// 不是怪物本体：怪物/NPC 只认紧贴包根目录的那一层文件。
func TestMobSubdirLookupTablesAreSkipped(t *testing.T) {
	rel := "Mob.wz/QuestCountGroup/0210100.img.xml"
	if got := extract.Infos(rel, load(t, rel)); len(got) != 0 {
		t.Errorf("QuestCountGroup 查表不该产出怪物属性，实际 %+v", got)
	}
	ok := extract.Infos("Mob.wz/0000021.img.xml", load(t, "Mob.wz/0000021.img.xml"))
	if len(ok) != 1 || ok[0].Info["level"] != "24" {
		t.Errorf("包根下的怪物文件仍应正常抽取: %+v", ok)
	}
}

// `Character.wz` 根级那 18 个文件没有类别目录，按 islot 归成皮肤：
// Bd 是身体皮肤、Hd 是纸娃娃基础头部（画布名分别是 body / head）。
func TestRootLevelSkinsGetCategoryFromIslot(t *testing.T) {
	cases := map[string]string{
		"Character.wz/00002001.img.xml": "BodySkin",
		"Character.wz/00012000.img.xml": "HeadSkin",
		// 有类别目录的不受影响，仍然按目录取名。
		"Character.wz/Accessory/01010000.img.xml": "Accessory",
	}
	for rel, want := range cases {
		got := extract.Infos(rel, load(t, rel))
		if len(got) != 1 {
			t.Fatalf("%s 应得 1 条，实际 %d", rel, len(got))
		}
		if got[0].Category != want {
			t.Errorf("%s 分类 = %q，期望 %q", rel, got[0].Category, want)
		}
	}
}

// 三个新域（技能 / 变身 / 反应堆）各自的数据形态：
//   - 技能：组文件再套一层（`skill/<7 位 ID>`），节点没有 info 子目录，数值在 level/1；
//   - 变身：4 位实体号，RootID 单独放宽到 4 位；无名称表；
//   - 反应堆：7 位一文件一实体；无名称表，数据里只有韩文 info。
func TestSkillMorphReactorDomains(t *testing.T) {
	// 技能名称来自 String.wz/Skill.img.xml（7 位键）。
	names := extract.Names("String.wz/Skill.img.xml", load(t, "String.wz/Skill.img.xml"))
	if hit := findByNameID(names, "00000008"); hit == nil {
		t.Fatalf("技能表未抽到 00000008（共 %d 条）", len(names))
	} else if hit.Name != "群宠" {
		t.Errorf("00000008 名称 = %q，期望 群宠", hit.Name)
	}
	// 组键 `000` 只有 bookName、没有 name，不该被当成技能实体产出行。
	if hit := findByNameID(names, "00000000"); hit != nil {
		t.Errorf("组键不该产出行，却抽到 %q", hit.Name)
	}

	// 技能属性：ID 在 skill/ 下，且节点没有 info 子目录。
	skills := extract.Infos("Skill.wz/000.img.xml", load(t, "Skill.wz/000.img.xml"))
	if len(skills) == 0 {
		t.Fatal("Skill.wz/000.img.xml 未产出技能条目")
	}
	s := findInfoByID(skills, "00001001")
	if s == nil {
		t.Fatalf("未抽到技能 00001001（共 %d 条）", len(skills))
	}
	if s.Kind != extract.KindSkill {
		t.Errorf("技能 Kind = %s，期望 %s", s.Kind, extract.KindSkill)
	}
	if _, ok := s.Info["level1.mpCon"]; !ok {
		t.Errorf("技能应含 level1.mpCon，实际 %v", s.Info)
	}

	// 变身：4 位实体号，RootID 对本包单独放宽。
	mroot := load(t, "Morph.wz/0001.img.xml")
	if got := extract.RootID("Morph.wz/0001.img.xml", mroot); got != "00000001" {
		t.Errorf("Morph RootID = %q，期望 00000001", got)
	}
	mi := extract.Infos("Morph.wz/0001.img.xml", mroot)
	if len(mi) != 1 || mi[0].Kind != extract.KindMorph {
		t.Fatalf("Morph 应产出 1 条 morph 记录，实际 %+v", mi)
	}
	if mi[0].Info["speed"] == "" {
		t.Errorf("Morph 应有 speed 属性，实际 %v", mi[0].Info)
	}

	// 反应堆：7 位一文件一实体。
	ri := extract.Infos("Reactor.wz/0002000.img.xml", load(t, "Reactor.wz/0002000.img.xml"))
	if len(ri) != 1 || ri[0].Kind != extract.KindReactor {
		t.Fatalf("Reactor 应产出 1 条 reactor 记录，实际 %+v", ri)
	}
	if ri[0].ID != "00002000" {
		t.Errorf("Reactor ID = %q，期望 00002000", ri[0].ID)
	}
}

func findInfoByID(list []extract.InfoEntry, id string) *extract.InfoEntry {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}
