package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	for _, k := range []string{"items", "named", "withInfo", "complete", "files", "iconsOn"} {
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
	if r2, _ := get(t, srv.URL+"/img/2000000.png"); r2.StatusCode != http.StatusNotFound {
		t.Errorf("未注册 /img 时应 404，实际 %d", r2.StatusCode)
	}
}
