package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sqx6781268/MapleWzMeta/internal/config"
	"github.com/sqx6781268/MapleWzMeta/internal/icons"
	"github.com/sqx6781268/MapleWzMeta/internal/store"
)

const testLang = "zh-CN"

// newTestServer 起一个带图标索引、含两个语言域的服务。
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	tmp := t.TempDir()
	db, err := store.Open(filepath.Join(tmp, "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.PutNames(testLang, []store.NameArg{
		{ID: "02000000", Name: "红色药水", Descr: "恢复 HP", Category: "Consume"},
		{ID: "01002000", Name: "骑士头盔", Category: "Cap"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PutInfos(testLang, []store.InfoArg{
		{ID: "02000000", Category: "Consume", Info: map[string]string{"slotMax": "100", "price": "50"}},
		{ID: "01002000", Category: "Cap", Info: map[string]string{"cash": "1", "reqLevel": "20", "icon.width": "30"}},
		{ID: "03999999", Category: "Etc", Info: map[string]string{"slotMax": "100"}},
	}); err != nil {
		t.Fatal(err)
	}
	// 另一语言只有属性，用来验证 lang 域确实隔离了数据。
	if _, err := db.PutInfos("en", []store.InfoArg{
		{ID: "02000000", Category: "Consume", Info: map[string]string{"slotMax": "100"}},
	}); err != nil {
		t.Fatal(err)
	}

	imgDir := filepath.Join(tmp, "imgdata")
	if err := os.MkdirAll(filepath.Join(imgDir, "Item", "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imgDir, "Item", "etc", "2000000.img.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := icons.Build(imgDir)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Locales = []config.Locale{
		{Lang: testLang, Label: "简体中文", Wz: tmp, Sources: config.DefaultSources},
		{Lang: "en", Label: "English", Wz: tmp, Sources: config.DefaultSources},
	}
	cfg.Icons = config.Icons{Enabled: true, Dir: imgDir}

	srv := httptest.NewServer(New(db, cfg, ix).Handler())
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	var out map[string]any
	if resp.StatusCode < 300 {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("解析 %s 失败: %v", url, err)
		}
	}
	return resp, out
}

func TestItemsSearch(t *testing.T) {
	srv := newTestServer(t)

	_, data := get(t, srv.URL+"/api/items?q=药水")
	if int(data["total"].(float64)) != 1 {
		t.Fatalf("名称模糊命中数错: %v", data["total"])
	}
	items := data["items"].([]any)
	it := items[0].(map[string]any)
	if it["id"] != "02000000" || it["name"] != "红色药水" || it["descr"] != "恢复 HP" {
		t.Errorf("返回内容错: %v", it)
	}
	if info := it["info"].(map[string]any); info["slotMax"] != "100" {
		t.Errorf("info 未带出: %v", info)
	}
	// ID 归一化后才能命中图标索引。
	if it["icon"] != "/img/02000000.png" {
		t.Errorf("图标字段错: %v", it["icon"])
	}

	// 纯数字关键字走 ID 前缀。
	_, data = get(t, srv.URL+"/api/items?q=01")
	if int(data["total"].(float64)) != 1 {
		t.Errorf("ID 前缀命中数错: %v", data["total"])
	}

	_, data = get(t, srv.URL+"/api/items?cat=Cap")
	if int(data["total"].(float64)) != 1 {
		t.Errorf("分类过滤错: %v", data["total"])
	}

	_, data = get(t, srv.URL+"/api/items?attr="+strings.TrimSpace("cash=1"))
	if int(data["total"].(float64)) != 1 {
		t.Errorf("属性过滤错: %v", data["total"])
	}

	_, data = get(t, srv.URL+"/api/items?attr=icon.width=30")
	if int(data["total"].(float64)) != 1 {
		t.Errorf("含点属性键过滤错: %v", data["total"])
	}

	_, data = get(t, srv.URL+"/api/items?min=reqLevel=20")
	if int(data["total"].(float64)) != 1 {
		t.Errorf("数值下界过滤错: %v", data["total"])
	}

	// complete=info 应当只剩缺名称的那条。
	_, data = get(t, srv.URL+"/api/items?complete=info")
	if int(data["total"].(float64)) != 1 {
		t.Errorf("缺名称筛选错: %v", data["total"])
	}
	_, data = get(t, srv.URL+"/api/items?complete=all")
	if int(data["total"].(float64)) != 2 {
		t.Errorf("齐全筛选错: %v", data["total"])
	}

	resp, _ := get(t, srv.URL+"/api/items?attr=%22bad%22=x")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("非法 attr 键应 400，实际 %d", resp.StatusCode)
	}
}

// TestLangScoping 验证同一 ID 在不同语言域下互不串台。
func TestLangScoping(t *testing.T) {
	srv := newTestServer(t)

	_, data := get(t, srv.URL+"/api/items?lang=en&q=0200")
	if int(data["total"].(float64)) != 1 {
		t.Fatalf("en 域命中数错: %v", data["total"])
	}
	it := data["items"].([]any)[0].(map[string]any)
	if it["name"] != "" || it["hasName"] != false {
		t.Errorf("en 域不应看到中文名称: %v", it)
	}

	resp, _ := get(t, srv.URL+"/api/items?lang=fr")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("未配置的语言应 400，实际 %d", resp.StatusCode)
	}

	_, data = get(t, srv.URL+"/api/langs")
	if data["default"] != testLang {
		t.Errorf("默认语言错: %v", data["default"])
	}
	langs := data["langs"].([]any)
	if len(langs) != 2 {
		t.Fatalf("语言清单长度错: %v", langs)
	}
	zh := langs[0].(map[string]any)
	if zh["items"].(float64) != 3 || zh["named"].(float64) != 2 {
		t.Errorf("zh-CN 规模错: %v", zh)
	}
	en := langs[1].(map[string]any)
	if en["items"].(float64) != 1 || en["label"] != "English" {
		t.Errorf("en 规模错: %v", en)
	}
}

// TestMeta 验证 /api/meta 给出中文说明、库内真实键，并屏蔽图形属性。
func TestMeta(t *testing.T) {
	srv := newTestServer(t)
	_, data := get(t, srv.URL+"/api/meta")

	cats := data["categories"].([]any)
	if len(cats) == 0 {
		t.Fatal("分类清单为空")
	}
	labels := map[string]string{}
	for _, c := range cats {
		m := c.(map[string]any)
		labels[m["name"].(string)] = m["label"].(string)
	}
	if labels["Cap"] != "帽子" {
		t.Errorf("Cap 的中文说明错: %v", labels)
	}

	attrs := map[string]map[string]any{}
	for _, a := range data["attrs"].([]any) {
		m := a.(map[string]any)
		attrs[m["key"].(string)] = m
	}
	if _, ok := attrs["icon.width"]; ok {
		t.Errorf("图形属性不该出现在筛选清单里: %v", attrs["icon.width"])
	}
	rl, ok := attrs["reqLevel"]
	if !ok {
		t.Fatalf("清单里没有 reqLevel: %v", keysOf(attrs))
	}
	if rl["label"] != "需求等级" || rl["kind"] != "int" || rl["preset"] != true {
		t.Errorf("reqLevel 说明错: %v", rl)
	}
	if int(rl["count"].(float64)) != 1 {
		t.Errorf("reqLevel 库内数量错: %v", rl["count"])
	}
	// 字典未收录的键要原样给出，前端才能支持自定义条件。
	if price, ok := attrs["cash"]; !ok || price["label"] != "商城道具" {
		t.Errorf("cash 说明错: %v", attrs["cash"])
	}
	if data["uncategorized"] != "(未分类)" {
		t.Errorf("未分类哨兵错: %v", data["uncategorized"])
	}

	resp, _ := get(t, srv.URL+"/api/meta?lang=fr")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("非法语言应 400，实际 %d", resp.StatusCode)
	}
}

func keysOf(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestExtendedAttrFilters 验证多个 ≥/≤、键存在、包含这几种条件能叠加。
func TestExtendedAttrFilters(t *testing.T) {
	srv := newTestServer(t)

	_, data := get(t, srv.URL+"/api/items?min=price=50&min=slotMax=100")
	if int(data["total"].(float64)) != 1 {
		t.Errorf("多个 ≥ 条件应取交集: %v", data["total"])
	}
	_, data = get(t, srv.URL+"/api/items?min=reqLevel=1&max=reqLevel=20")
	if int(data["total"].(float64)) != 1 {
		t.Errorf("≥ + ≤ 区间错: %v", data["total"])
	}
	// 缺该键的物品不该被 ≤ 命中（否则等于把 0 当合法值）。
	_, data = get(t, srv.URL+"/api/items?max=price=1000")
	if int(data["total"].(float64)) != 1 {
		t.Errorf("≤ 命中数错: %v", data["total"])
	}
	_, data = get(t, srv.URL+"/api/items?has=icon.width")
	if int(data["total"].(float64)) != 1 {
		t.Errorf("has 只看键存在，结果错: %v", data["total"])
	}
	_, data = get(t, srv.URL+"/api/items?like=price=5")
	if int(data["total"].(float64)) != 1 {
		t.Errorf("like 包含匹配错: %v", data["total"])
	}
	_, data = get(t, srv.URL+"/api/items?has=cash&min=reqLevel=10&attr=cash=1")
	if int(data["total"].(float64)) != 1 {
		t.Errorf("多类条件叠加错: %v", data["total"])
	}

	resp, _ := get(t, srv.URL+"/api/items?min=price=abc")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("min 非整数应 400，实际 %d", resp.StatusCode)
	}
	resp, _ = get(t, srv.URL+"/api/items?has=%22bad%22")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("has 非法键应 400，实际 %d", resp.StatusCode)
	}
}

func TestItemDetailAnd404(t *testing.T) {
	srv := newTestServer(t)
	_, data := get(t, srv.URL+"/api/item?id=01002000")
	if data["name"] != "骑士头盔" || data["hasName"] != true || data["hasInfo"] != true {
		t.Errorf("详情错: %v", data)
	}
	// 无对应图片的属性条目不该带 icon 字段。
	if _, ok := data["icon"]; ok {
		t.Errorf("无图物品不该返回 icon: %v", data)
	}
	resp, _ := get(t, srv.URL+"/api/item?id=99999999")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("不存在的 ID 应 404，实际 %d", resp.StatusCode)
	}
	resp, _ = get(t, srv.URL+"/api/item?id=01002000&lang=en")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("en 域缺该 ID，应 404，实际 %d", resp.StatusCode)
	}
}

func TestStatsAndIndex(t *testing.T) {
	srv := newTestServer(t)
	_, data := get(t, srv.URL+"/api/stats")
	for _, k := range []string{"items", "named", "withInfo", "complete", "files", "iconsOn", "adminOn"} {
		if _, ok := data[k]; !ok {
			t.Errorf("stats 缺字段 %s: %v", k, data)
		}
	}
	if int(data["items"].(float64)) != 3 {
		t.Errorf("物品总数错: %v", data["items"])
	}
	if data["iconsOn"] != true {
		t.Errorf("iconsOn 错: %v", data["iconsOn"])
	}
	// 首页的管理入口靠 adminOn 决定是否显示，未配置时按开处理。
	if data["adminOn"] != true {
		t.Errorf("adminOn 错: %v", data["adminOn"])
	}

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("首页状态码 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("首页类型错: %s", ct)
	}
}

// TestIconRoute 验证 /img 提供真实文件，且拒绝越界路径。
func TestIconRoute(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.Get(srv.URL + "/img/2000000.png")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("图标应 200，实际 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "image") {
		t.Errorf("图标类型错: %s", ct)
	}
	for _, u := range []string{"/img/%2e%2e/%2e%2e/x.png", "/img/notanid.png"} {
		r, err := http.Get(srv.URL + u)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode >= 500 {
			t.Errorf("%s 不该 5xx，实际 %d", u, r.StatusCode)
		}
		if r.StatusCode < 400 || r.StatusCode >= 500 {
			t.Errorf("%s 应 4xx，实际 %d", u, r.StatusCode)
		}
	}
}

// TestIconsDisabled 验证关掉图标时既不返回 icon 字段，也不注册 /img。
func TestIconsDisabled(t *testing.T) {
	tmp := t.TempDir()
	db, err := store.Open(filepath.Join(tmp, "web.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.PutInfos(testLang, []store.InfoArg{
		{ID: "02000000", Category: "Consume", Info: map[string]string{"slotMax": "100"}},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Locales = []config.Locale{{Lang: testLang, Label: "简体中文", Wz: tmp}}
	off := false
	cfg.Admin = config.Admin{Enabled: &off}
	srv := httptest.NewServer(New(db, cfg, nil).Handler())
	defer srv.Close()

	_, data := get(t, srv.URL+"/api/items")
	it := data["items"].([]any)[0].(map[string]any)
	if _, ok := it["icon"]; ok {
		t.Errorf("图标关闭时不该有 icon 字段: %v", it)
	}
	_, stats := get(t, srv.URL+"/api/stats")
	if stats["iconsOn"] != false {
		t.Errorf("iconsOn 应为 false: %v", stats)
	}
	if stats["adminOn"] != false {
		t.Errorf("admin.enabled=false 时 adminOn 应为 false: %v", stats)
	}
	if r2, _ := get(t, srv.URL+"/img/2000000.png"); r2.StatusCode != http.StatusNotFound {
		t.Errorf("未注册 /img 时应 404，实际 %d", r2.StatusCode)
	}
}

// kind 参数贯通检索 / 详情 / 统计 / 图标域：同一个 8 位 ID 在三个域各自成行，
// 物品行绝不挂 NPC 立绘（实测那会串味 1,578 行）。
func TestKindParamAndIconDomain(t *testing.T) {
	tmp := t.TempDir()
	db, err := store.Open(filepath.Join(tmp, "kind.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.PutNames(testLang, []store.NameArg{
		{Kind: store.KindItem, ID: "01000000", Name: "布帽", Category: "Cap"},
		{Kind: store.KindMob, ID: "01110100", Name: "绿蘑菇"},
		{Kind: store.KindNPC, ID: "01110100", Name: "明珠港出租车"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PutInfos(testLang, []store.InfoArg{
		{Kind: store.KindMob, ID: "01110100", Info: map[string]string{"level": "1"}},
	}); err != nil {
		t.Fatal(err)
	}

	imgDir := filepath.Join(tmp, "imgdata")
	for _, rel := range []string{"Character/Cap/01000000.img.png", "Npc/1110100.img.png"} {
		p := filepath.Join(imgDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := icons.Build(imgDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Locales = []config.Locale{{Lang: testLang, Label: "简体中文", Wz: tmp}}
	srv := httptest.NewServer(New(db, cfg, ix).Handler())
	t.Cleanup(srv.Close)

	if r, _ := get(t, srv.URL+"/api/items?kind=bogus"); r.StatusCode != http.StatusBadRequest {
		t.Errorf("陌生 kind 应 400，实际 %d", r.StatusCode)
	}

	// 物品域：只有布帽，且拿到装备图；同 ID 的怪物/NPC 行不参与。
	_, items := get(t, srv.URL+"/api/items")
	if int(items["total"].(float64)) != 1 {
		t.Fatalf("物品域应 1 条: %v", items)
	}
	row0 := items["items"].([]any)[0].(map[string]any)
	if row0["icon"] != "/img/01000000.png" {
		t.Errorf("物品图标路径错: %v", row0["icon"])
	}
	if r, _ := get(t, srv.URL+"/api/item?id=01110100"); r.StatusCode != http.StatusNotFound {
		t.Errorf("物品域不该有 01110100，实际 %d", r.StatusCode)
	}

	cases := []struct {
		kind, wantName string
		wantIcon       any
	}{
		{string(store.KindMob), "绿蘑菇", nil},
		{string(store.KindNPC), "明珠港出租车", "/img/01110100.png?for=npc"},
	}
	for _, c := range cases {
		_, list := get(t, srv.URL+"/api/items?kind="+c.kind)
		rows := list["items"].([]any)
		if len(rows) != 1 {
			t.Fatalf("%s 域应 1 条，实际 %v", c.kind, list)
		}
		r0 := rows[0].(map[string]any)
		if r0["name"] != c.wantName {
			t.Errorf("%s 域名称错: %v", c.kind, r0["name"])
		}
		if r0["icon"] != c.wantIcon {
			t.Errorf("%s 域图标错: 期望 %v 实际 %v", c.kind, c.wantIcon, r0["icon"])
		}
		_, st := get(t, srv.URL+"/api/stats?kind="+c.kind)
		if st["kind"] != c.kind || int(st["items"].(float64)) != 1 {
			t.Errorf("%s 域概况错: %v", c.kind, st)
		}
		// 详情按 7 位原始号也能取到（补零归一在存储层做）。
		resp, err := http.Get(srv.URL + "/api/item?kind=" + c.kind + "&id=1110100")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s 域 7 位 ID 详情应 200，实际 %d", c.kind, resp.StatusCode)
		}
	}

	// 只存在于 Npc 域的图：缺省（物品）404，?for=npc 才 200。
	for _, c := range []struct {
		url  string
		want int
	}{
		{"/img/01110100.png", http.StatusNotFound},
		{"/img/01110100.png?for=npc", http.StatusOK},
		{"/img/01000000.png?for=npc", http.StatusNotFound},
	} {
		resp, err := http.Get(srv.URL + c.url)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s 应 %d，实际 %d", c.url, c.want, resp.StatusCode)
		}
	}
}

// 详情接口要额外给出名称侧与属性侧的来源文件；列表接口不带（反查要扫全表 JSON）。
func TestDetailCarriesSourceFiles(t *testing.T) {
	tmp := t.TempDir()
	db, err := store.Open(filepath.Join(tmp, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.PutNames(testLang, []store.NameArg{
		{Kind: store.KindItem, ID: "01000001", Name: "黑福巾", Category: "Cap"},
		{Kind: store.KindMob, ID: "01000001", Name: "蜗牛"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PutInfos(testLang, []store.InfoArg{
		{Kind: store.KindItem, ID: "01000001", Category: "Cap", Info: map[string]string{"islot": "Cp"}},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	if err := db.SaveFiles(testLang, []store.FileRec{
		{Path: "String.wz/Eqp.img.xml", Size: 1, ModTime: now, Status: "ok", Kind: store.KindItem, IDs: []string{"01000001"}},
		{Path: "Character.wz/Cap/01000001.img.xml", Size: 1, ModTime: now, Status: "ok", Kind: store.KindItem, IDs: []string{"01000001"}},
		{Path: "String.wz/Mob.img.xml", Size: 1, ModTime: now, Status: "ok", Kind: store.KindMob, IDs: []string{"01000001"}},
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Locales = []config.Locale{{Lang: testLang, Label: "简体中文", Wz: tmp}}
	srv := httptest.NewServer(New(db, cfg, nil).Handler())
	t.Cleanup(srv.Close)

	_, it := get(t, srv.URL+"/api/item?lang="+testLang+"&id=1000001")
	sf, _ := it["stringFiles"].([]any)
	ifz, _ := it["infoFiles"].([]any)
	if len(sf) != 1 || sf[0] != "String.wz/Eqp.img.xml" {
		t.Errorf("详情缺名称侧来源: %v", it["stringFiles"])
	}
	if len(ifz) != 1 || ifz[0] != "Character.wz/Cap/01000001.img.xml" {
		t.Errorf("详情缺属性侧来源: %v", it["infoFiles"])
	}
	// 怪物侧只有名称文件，属性侧应为空——跨域不得互认来源。
	_, mob := get(t, srv.URL+"/api/item?kind=mob&lang="+testLang+"&id=01000001")
	if m, _ := mob["infoFiles"].([]any); len(m) != 0 {
		t.Errorf("怪物域不该有物品侧来源: %v", mob["infoFiles"])
	}
	// 列表接口不带溯源，避免每行都做一次全表 JSON 展开。
	_, list := get(t, srv.URL+"/api/items?lang="+testLang)
	row0 := list["items"].([]any)[0].(map[string]any)
	if _, ok := row0["stringFiles"]; ok {
		t.Errorf("列表行不该带 stringFiles: %v", row0)
	}
}
