package wzxml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testdata(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("testdata", name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("缺少测试数据 %s: %v", p, err)
	}
	return p
}

func decodeTestdata(t *testing.T, name string) *Node {
	t.Helper()
	n, err := DecodeFile(testdata(t, name))
	if err != nil {
		t.Fatalf("解码 %s 失败: %v", name, err)
	}
	return n
}

// 多行缩进式导出（Item.wz/Etc）：canvas 带 width/height，值全部在属性上。
func TestDecodeItemEtc(t *testing.T) {
	root := decodeTestdata(t, "item_etc_0430.img.xml")

	if root.Type != "imgdir" || root.Name != "0430.img" {
		t.Fatalf("根节点不符: type=%q name=%q", root.Type, root.Name)
	}
	if got := len(root.Children); got != 2 {
		t.Fatalf("子物品数应为 2，实际 %d", got)
	}

	item := root.Child("04300000")
	if item == nil {
		t.Fatal("缺少物品节点 04300000")
	}
	info := item.Find("info")
	if info == nil {
		t.Fatal("缺少 info 节点")
	}

	for _, tc := range []struct {
		key  string
		want int64
	}{
		{"slotMax", 1}, {"tradeBlock", 1}, {"price", 1}, {"notSale", 1}, {"timeLimited", 1},
	} {
		v, ok := info.IntValue(tc.key)
		if !ok || v != tc.want {
			t.Errorf("info.%s = %d,%v 期望 %d", tc.key, v, ok, tc.want)
		}
	}

	icon := info.Child("icon")
	if icon == nil || icon.Type != "canvas" {
		t.Fatalf("icon 节点异常: %+v", icon)
	}
	if icon.Attr("width") != "26" || icon.Attr("height") != "31" {
		t.Errorf("icon 尺寸 = %q x %q，期望 26 x 31", icon.Attr("width"), icon.Attr("height"))
	}
	if o := icon.Child("origin"); o == nil || o.Type != "vector" || o.Attr("x") != "-3" || o.Attr("y") != "31" {
		t.Errorf("origin 向量异常: %+v", o)
	}
	// name/value 不进入 Attrs，避免语义字段与扩展字段混在一起。
	if _, ok := icon.Attrs["name"]; ok {
		t.Error("name 不应出现在 Attrs 中")
	}
	if _, ok := info.Child("slotMax").Attrs["value"]; ok {
		t.Error("value 不应出现在 Attrs 中")
	}
}

// 另一套导出器（Item.wz/Etc 大文件）的 canvas 只有 format/scale，没有 width/height，
// 根节点还带 indent/media，模型必须原样保留而不下结论。
func TestDecodeFormatScaleVariant(t *testing.T) {
	root := decodeTestdata(t, "item_canvas_format_scale.img.xml")
	if root.Attr("indent") != "2" || root.Attr("media") != "NONE" {
		t.Errorf("根节点扩展属性丢失: %+v", root.Attrs)
	}
	icon := root.Find("04000000", "info", "icon")
	if icon == nil || icon.Type != "canvas" {
		t.Fatal("icon 节点丢失")
	}
	if icon.Attr("width") != "" || icon.Attr("height") != "" {
		t.Errorf("该导出器不应出现 width/height: %+v", icon.Attrs)
	}
	if icon.Attr("format") != "2" || icon.Attr("scale") != "0" {
		t.Errorf("format/scale 未保留: %+v", icon.Attrs)
	}
	info := root.Find("04000000", "info")
	if v, ok := info.IntValue("price"); !ok || v != 3 {
		t.Errorf("price = %d,%v 期望 3, true", v, ok)
	}
}

// 单行式导出（Character.wz）+ uol 打平。
func TestDecodeCharacterAndResolveUol(t *testing.T) {
	root := decodeTestdata(t, "character_accessory_01010000.img.xml")

	if root.Name != "01010000.img" {
		t.Fatalf("根节点 name = %q", root.Name)
	}
	def := root.Find("default", "default")
	if def == nil || def.Type != "canvas" {
		t.Fatal("缺少 default/default 画布节点")
	}
	before := root.Count()
	if before.ByType["uol"] == 0 {
		t.Fatal("样例中应存在未解析的 uol 节点")
	}

	st, err := ResolveUol(root)
	if err != nil {
		t.Fatalf("uol 打平失败: %v", err)
	}
	if st.Found == 0 || st.Resolved == 0 {
		t.Fatalf("uol 统计异常: %+v", st)
	}
	if st.Unresolved != 0 {
		t.Errorf("仍有 %d 个 uol 未解析", st.Unresolved)
	}

	// blink/0/default 引用 ../../default/default，打平后应与目标同形。
	blink := root.Find("blink", "0", "default")
	if blink == nil {
		t.Fatal("blink/0/default 丢失")
	}
	if blink.Type != "canvas" {
		t.Errorf("blink/0/default 类型 = %q，期望 canvas", blink.Type)
	}
	if blink.Origin != "../../default/default" {
		t.Errorf("Origin = %q，期望保留来源路径", blink.Origin)
	}
	if blink.Name != "default" {
		t.Errorf("打平后应保留 uol 自身 name，实际 %q", blink.Name)
	}
	if string(blink.JSON()) != string(def.JSON()) {
		// 目标本身没有 Origin，克隆后新增的 Origin 需剔除再比较内容。
		clone := blink.Clone()
		clone.Origin = ""
		if string(clone.JSON()) != string(def.JSON()) {
			t.Errorf("打平内容与目标不一致:\n got=%s\nwant=%s", clone.JSON(), def.JSON())
		}
	}

	// 多层引用（stunned/0/default -> ../../bewildered/0/default，后者自身也是 uol）应被逐趟解开。
	stunned := root.Find("stunned", "0", "default")
	if stunned == nil {
		t.Fatal("stunned/0/default 丢失")
	}
	if stunned.Type != "canvas" {
		t.Errorf("嵌套 uol 未完全展开，类型 = %q", stunned.Type)
	}

	after := root.Count()
	if after.Nodes <= before.Nodes {
		t.Errorf("打平后节点数应增长: %d -> %d", before.Nodes, after.Nodes)
	}
	if after.ByType["uol"] != 0 {
		t.Errorf("打平后仍残留 uol: %d", after.ByType["uol"])
	}
}

func TestResolveUolKeepsMissingTarget(t *testing.T) {
	root := decodeTestdata(t, "uol_cross_img.img.xml")
	st, err := ResolveUol(root)
	if err != nil {
		t.Fatalf("打平失败: %v", err)
	}
	if st.Unresolved != 1 {
		t.Errorf("跨 img 引用应记为未解析，实际 %+v", st)
	}
	n := root.Find("info", "icon")
	if n == nil || n.Type != "uol" || n.Value != "../../Other.img/x" {
		t.Errorf("未解析的 uol 应保持原样，实际 %+v", n)
	}
}

func TestResolveUolCycleGuard(t *testing.T) {
	root := decodeTestdata(t, "uol_cycle.img.xml")
	st, err := ResolveUol(root)
	if err != nil {
		t.Fatalf("打平不应报错: %v", err)
	}
	if st.Found == 0 {
		t.Fatal("未发现 uol 节点")
	}
	if st.Unresolved == 0 {
		t.Error("自引用环应留下未解析节点，而不是全部成功")
	}
	if c := root.Count(); c.Nodes > 100000 {
		t.Errorf("环保护失效，节点爆炸到 %d", c.Nodes)
	}
}

func TestResolveUolRelativeSemantics(t *testing.T) {
	root := decodeTestdata(t, "uol_paths.img.xml")
	st, err := ResolveUol(root)
	if err != nil {
		t.Fatalf("打平失败: %v", err)
	}
	if st.Found != 5 || st.Unresolved != 0 {
		t.Fatalf("uol 统计异常: %+v", st)
	}
	cases := map[string]string{
		"a/froma":         "a-str",
		"a/b/samelevel":   "b-str",
		"a/b/d/upone":     "b-str",
		"a/b/d/stay":      "d-str",
		"a/b/d/rootlevel": "root-str",
	}
	for path, want := range cases {
		n := root.Find(strings.Split(path, "/")...)
		if n == nil {
			t.Errorf("%s 丢失", path)
			continue
		}
		if n.Type != "string" || n.Value != want {
			t.Errorf("%s 解析为 %s/%q，期望 string/%q", path, n.Type, n.Value, want)
		}
	}
}

// 实测脏数据形态：引用值按文件根书写但没写 ../，父相对解析不到时需根相对兜底。
func TestResolveUolRootRelativeFallback(t *testing.T) {
	root := decodeTestdata(t, "uol_root_fallback.img.xml")
	st, err := ResolveUol(root)
	if err != nil {
		t.Fatalf("打平失败: %v", err)
	}
	if st.Found != 2 || st.Resolved != 2 || st.Unresolved != 0 {
		t.Fatalf("统计异常: %+v", st)
	}
	if st.RootFixed != 1 {
		t.Errorf("RootFixed = %d，期望 1（只有 default/default 走兜底）", st.RootFixed)
	}
	n := root.Find("smile", "0", "default")
	if n == nil || n.Type != "string" || n.Value != "root-target" {
		t.Errorf("兜底结果异常: %+v", n)
	}
	if n.Origin != "default/default" {
		t.Errorf("应保留来源路径，实际 %q", n.Origin)
	}
	// ../0/default 这类正常相对引用不应受兜底影响。
	n1 := root.Find("smile", "1", "default")
	if n1 == nil || n1.Value != "root-target" {
		t.Errorf("链式引用解析失败: %+v", n1)
	}
}

// 解码 -> WriteXML -> 再解码，树内容必须不变；这是导出回写的正确性前提。
func TestXMLRoundTripStable(t *testing.T) {
	for _, f := range []string{"item_etc_0430.img.xml", "item_canvas_format_scale.img.xml", "character_accessory_01010000.img.xml"} {
		root := decodeTestdata(t, f)
		if _, err := ResolveUol(root); err != nil {
			t.Fatalf("%s uol 打平失败: %v", f, err)
		}
		// WriteXML 不输出 Origin（它是 uol 打平派生的元信息），比较前剥离。
		root.Walk(func(_, n *Node) bool { n.Origin = ""; return true })
		want := root.JSON()

		var sb strings.Builder
		sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
		if err := root.WriteXML(&sb); err != nil {
			t.Fatalf("%s 序列化失败: %v", f, err)
		}
		again, err := Decode(strings.NewReader(sb.String()))
		if err != nil {
			t.Fatalf("%s 回读失败: %v", f, err)
		}
		if got := again.JSON(); string(got) != string(want) {
			t.Errorf("%s 往返不一致\nlen got=%d want=%d", f, len(got), len(want))
		}
	}
}

// 确定性 JSON：键序固定，重复序列化字节一致，才能做统计比对与 golden。
func TestJSONDeterministic(t *testing.T) {
	root := &Node{
		Type: "imgdir", Name: "00001000.img",
		Attrs: map[string]string{"zz": "1", "aa": "2", "media": "NONE"},
		Children: []*Node{
			{Type: "int", Name: "price", Value: "100"},
			{Type: "imgdir", Name: "info", Children: []*Node{{Type: "string", Name: "x", Value: "中文 & \"引号\" <标签>"}}},
		},
	}
	first := string(root.JSON())
	for i := 0; i < 50; i++ {
		if got := string(root.JSON()); got != first {
			t.Fatalf("第 %d 次序列化不一致:\n%s\n%s", i, got, first)
		}
	}
	// Attrs 按字典序输出。
	if !strings.Contains(first, `"_a":{"aa":"2","media":"NONE","zz":"1"}`) {
		t.Errorf("属性键序不按字典序: %s", first)
	}
	if !strings.Contains(first, `"_v":"中文 & \"引号\" <标签>"`) {
		t.Errorf("转义异常: %s", first)
	}
}

func TestDecodeToleratesLooseXML(t *testing.T) {
	// 未定义实体与多余声明：Strict=false 下不应 panic，且应能取到节点。
	src := `<?xml version="1.0" encoding="UTF-8"?><imgdir name="A"><string name="v" value="&nbsp;"/></imgdir>`
	root, err := Decode(strings.NewReader(src))
	if err != nil {
		t.Logf("宽松模式仍报错（可接受，交由上层记录跳过）: %v", err)
		return
	}
	if root.Child("v") == nil {
		t.Error("未取到子节点")
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := map[string]string{
		"空文件":    "",
		"仅声明":    `<?xml version="1.0" encoding="UTF-8"?>`,
		"未闭合":    `<imgdir name="A"><int name="b" value="1"/></imgdi>`,
		"多根":     `<imgdir name="A"/><imgdir name="B"/>`,
		"二进制垃圾":  "\x00\x01\x02\x03",
		"根节点自封闭": `<imgdir name="A"/>`,
	}
	for name, src := range cases {
		_, err := Decode(strings.NewReader(src))
		switch name {
		case "根节点自封闭":
			if err != nil {
				t.Errorf("%s: 合法输入报错: %v", name, err)
			}
		default:
			if err == nil {
				t.Errorf("%s: 期望报错却成功", name)
			}
			if strings.Contains(err.Error(), "\x00") {
				t.Errorf("%s: 错误信息含不可见字符", name)
			}
		}
	}
}

// WZ 老数据里存在截断的中文（非法 UTF-8 字节），归档 JSON 必须原样保留而不是变成 U+FFFD。
func TestJSONPreservesInvalidUTF8(t *testing.T) {
	raw := "红色药草\xed\x80" // 末尾是不完整的三字节序列
	n := &Node{Type: "string", Name: "desc", Value: raw}
	got := string(n.JSON())
	if !strings.Contains(got, raw) {
		t.Errorf("非法字节被改写:\n got=%q\nwant=%q", got, raw)
	}
	if strings.Contains(got, "\ufffd") {
		t.Errorf("出现了替换字符: %q", got)
	}
}

func TestCountAndHelpers(t *testing.T) {
	root := decodeTestdata(t, "item_etc_0430.img.xml")
	c := root.Count()
	if c.Nodes < 20 || c.MaxDepth < 4 {
		t.Errorf("统计异常: %+v", c)
	}
	if c.ByType["imgdir"] == 0 || c.ByType["canvas"] == 0 {
		t.Errorf("类型计数异常: %+v", c.ByType)
	}
	if got := root.ChildAt(-1); got != nil {
		t.Error("ChildAt 越界应返回 nil")
	}
	if got := root.Child("不存在"); got != nil {
		t.Error("Child 未命中应返回 nil")
	}
	names := strings.Join(root.ChildNames(), ",")
	if names != "04300000,04301000" {
		t.Errorf("ChildNames = %q", names)
	}
	// IntValue 走 hexvalue 兜底。
	h := &Node{Type: "imgdir", Children: []*Node{{Type: "int", Name: "color", Value: "abc", Attrs: map[string]string{"hexvalue": "ff00"}}}}
	if v, ok := h.IntValue("color"); !ok || v != 0xff00 {
		t.Errorf("hexvalue 兜底失败: %d %v", v, ok)
	}
	// Clone 深拷贝。
	cl := root.Clone()
	cl.Children[0].Name = "改过"
	if root.Children[0].Name == "改过" {
		t.Error("Clone 未深拷贝子节点")
	}
	// Walk 提前中止。
	seen := 0
	root.Walk(func(_, _ *Node) bool {
		seen++
		return seen < 3
	})
	if seen != 3 {
		t.Errorf("Walk 未按 false 中止，seen=%d", seen)
	}
}

func TestStripBOM(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bom.img.xml")
	content := append([]byte("\xef\xbb\xbf"), []byte(`<imgdir name="A"><int name="x" value="1"/></imgdir>`)...)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := DecodeFile(p)
	if err != nil {
		t.Fatalf("带 BOM 文件解码失败: %v", err)
	}
	if root.Name != "A" {
		t.Errorf("根 name = %q", root.Name)
	}
}

// 真实导出树抽样：Character/Accessory 前 300 个文件全部解码并打平 uol，不允许出现错误。
// 需要完整数据目录，go test -short 时跳过。
func TestRealTreeSample(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实数据抽样")
	}
	root := os.Getenv("MAPLEWZMETA_WZ")
	if root == "" {
		root = filepath.Join("..", "..", "wz")
	}
	dir := filepath.Join(root, "Character.wz", "Accessory")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("无真实数据目录 %s: %v", dir, err)
	}
	limit := 300
	if len(ents) < limit {
		limit = len(ents)
	}
	var totalUol, unresolved, files int
	for _, e := range ents[:limit] {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".img.xml") {
			continue
		}
		n, err := DecodeFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Errorf("%s 解码失败: %v", e.Name(), err)
			continue
		}
		st, err := ResolveUol(n)
		if err != nil {
			t.Errorf("%s uol 打平失败: %v", e.Name(), err)
			continue
		}
		files++
		totalUol += st.Found
		unresolved += st.Unresolved
	}
	if files == 0 {
		t.Fatal("没有抽样到任何文件")
	}
	t.Logf("抽样 %d 个文件，uol 共 %d 个，未解析 %d 个", files, totalUol, unresolved)
}
