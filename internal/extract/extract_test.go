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
	if !extract.LooksLikeItemID("1010000") || extract.LooksLikeItemID("100100") ||
		extract.LooksLikeItemID("101000") || extract.LooksLikeItemID("abc12345") {
		t.Error("LooksLikeItemID 判定不符：只接受 7 或 8 位纯数字")
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
		names := extract.Names(load(t, c.file))
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
	names := extract.Names(load(t, "String.wz/Eqp.img.xml"))
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
