package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sqx6781268/MapleWzMeta/internal/config"
	"github.com/sqx6781268/MapleWzMeta/internal/icons"
	"github.com/sqx6781268/MapleWzMeta/internal/scan"
	"github.com/sqx6781268/MapleWzMeta/internal/store"
)

const adminToken = "s3cr3t"

const (
	strXMLFull = `<?xml version="1.0" encoding="UTF-8"?><imgdir name="String.wz/Consume.img"><imgdir name="4000000"><string name="name" value="蓝色蜗牛壳"/></imgdir><imgdir name="2000000"><string name="name" value="红色药水"/></imgdir></imgdir>`
	strXMLSlim = `<?xml version="1.0" encoding="UTF-8"?><imgdir name="String.wz/Consume.img"><imgdir name="2000000"><string name="name" value="红色药水"/></imgdir></imgdir>`

	infoXMLFull = `<?xml version="1.0" encoding="UTF-8"?><imgdir name="0430.img"><imgdir name="4000000"><imgdir name="info"><int name="slotMax" value="100"/><int name="price" value="1"/></imgdir></imgdir></imgdir>`
	// 属性侧改过：price 这个键没了，slotMax 换了值。
	infoXMLAltered = `<?xml version="1.0" encoding="UTF-8"?><imgdir name="0430.img"><imgdir name="4000000"><imgdir name="info"><int name="slotMax" value="50"/></imgdir></imgdir></imgdir>`
)

// adminFixture 是一套真在临时目录里落 XML 与 .img.png 的管理端测试环境。
type adminFixture struct {
	srv  *httptest.Server
	db   *store.DB
	root string // WZ 导出根
}

func newAdminServer(t *testing.T, ad config.Admin) *adminFixture {
	t.Helper()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "wz")
	for rel, body := range map[string]string{
		"String.wz/Consume.img.xml": strXMLFull,
		"Item.wz/Etc/0430.img.xml":  infoXMLFull,
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	imgDir := filepath.Join(tmp, "imgdata")
	if err := os.MkdirAll(filepath.Join(imgDir, "Item", "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 真落一张 PNG：管理接口返回的物品视图走同一套图标解析，不用 mock。
	if err := os.WriteFile(filepath.Join(imgDir, "Item", "etc", "2000000.img.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := icons.Build(imgDir)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(tmp, "admin.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := scan.Run(scan.Options{Lang: testLang, Root: root, Quiet: true}, db); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Locales = []config.Locale{{Lang: testLang, Label: "简体中文", Wz: root, Sources: config.DefaultSources}}
	cfg.Icons = config.Icons{Enabled: true, Dir: imgDir}
	cfg.Admin = ad
	srv := httptest.NewServer(New(db, cfg, ix).Handler())
	t.Cleanup(srv.Close)
	return &adminFixture{srv: srv, db: db, root: root}
}

func withToken() config.Admin { return config.Admin{Token: adminToken} }

func (f *adminFixture) write(t *testing.T, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// call 打一次请求，返回状态码、JSON（数组会被塞进 "_array"）与原始文本。
func (f *adminFixture) call(t *testing.T, method, path, body, token string) (int, map[string]any, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("X-Admin-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	switch {
	case len(raw) > 0 && raw[0] == '{':
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("解析 %s %s 失败: %v（%s）", method, path, err, raw)
		}
	case len(raw) > 0 && raw[0] == '[':
		var arr []any
		if err := json.Unmarshal(raw, &arr); err != nil {
			t.Fatalf("解析 %s %s 失败: %v", method, path, err)
		}
		out["_array"] = arr
	}
	return resp.StatusCode, out, string(raw)
}

func (f *adminFixture) mustCall(t *testing.T, method, path, body string) map[string]any {
	t.Helper()
	code, out, raw := f.call(t, method, path, body, adminToken)
	if code >= 300 {
		t.Fatalf("%s %s 应成功，实际 %d: %s", method, path, code, raw)
	}
	return out
}

// waitJob 等后台任务收尾，返回最后一次快照。
func (f *adminFixture) waitJob(t *testing.T) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		d := f.mustCall(t, "GET", "/api/admin/job", "")
		if j, ok := d["job"].(map[string]any); ok {
			if s := j["state"]; s != "running" && s != "queued" {
				return j
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("等管理任务超时: %+v", d)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAdminDiffAndReload(t *testing.T) {
	f := newAdminServer(t, withToken())

	d := f.mustCall(t, "GET", "/api/admin/diff?lang="+testLang, "")
	if d["unchanged"] != float64(2) || d["new"] != float64(0) || d["gone"] != float64(0) {
		t.Fatalf("初始比对应全未变: %v", d)
	}
	if d["provenance"] != true {
		t.Errorf("首次扫描后应已有文件级溯源: %v", d["provenance"])
	}
	if d["storedFiles"] != float64(2) || d["diskFiles"] != float64(2) {
		t.Errorf("总量错: %v", d)
	}

	// 模拟"XML 变动"：名称侧删掉一个 ID。
	f.write(t, "String.wz/Consume.img.xml", strXMLSlim)
	d = f.mustCall(t, "GET", "/api/admin/diff?lang="+testLang, "")
	if d["changed"] != float64(1) {
		t.Fatalf("改动后应检出 1 个变更: %v", d)
	}
	if len(d["rows"].([]any)) != 1 {
		t.Errorf("明细应只给异常一类: %v", d["rows"])
	}

	f.mustCall(t, "POST", "/api/admin/reload?lang="+testLang, `{"selector":"changed"}`)
	job := f.waitJob(t)
	if job["state"] != "done" {
		t.Fatalf("任务应成功: %v", job)
	}
	sum := job["summary"].(map[string]any)
	if sum["parsed"] != float64(1) || sum["cleared"] != float64(1) {
		t.Errorf("任务统计错: %v", sum)
	}
	if it, _, err := f.db.Get(store.KindItem, testLang, "04000000"); err != nil || it.HasName {
		t.Errorf("名称没跟着 XML 清除: %+v err=%v", it, err)
	}
	if it, _, err := f.db.Get(store.KindItem, testLang, "02000000"); err != nil || it.Name != "红色药水" {
		t.Errorf("仍在的名称不该动: %+v err=%v", it, err)
	}
	if it, _, err := f.db.Get(store.KindItem, testLang, "04000000"); err != nil || it.Info["price"] != "1" {
		t.Errorf("未重载文件的属性被误改: %+v err=%v", it, err)
	}
	d = f.mustCall(t, "GET", "/api/admin/diff?lang="+testLang, "")
	if d["unchanged"] != float64(2) || d["changed"] != float64(0) {
		t.Errorf("重载后应重新一致: %v", d)
	}

	// 属性侧整包替换：price 消失、slotMax 换值。
	f.write(t, "Item.wz/Etc/0430.img.xml", infoXMLAltered)
	f.mustCall(t, "POST", "/api/admin/reload?lang="+testLang, `{"paths":["Item.wz/Etc/0430.img.xml"]}`)
	if job := f.waitJob(t); job["state"] != "done" {
		t.Fatalf("属性侧重载失败: %v", job)
	}
	it, _, err := f.db.Get(store.KindItem, testLang, "04000000")
	if err != nil || it.Info["slotMax"] != "50" {
		t.Fatalf("info 没整包替换: %+v err=%v", it, err)
	}
	if _, ok := it.Info["price"]; ok {
		t.Errorf("XML 里已删除的键没被清掉: %+v", it.Info)
	}
}

func TestAdminFilesAndRunsListing(t *testing.T) {
	f := newAdminServer(t, withToken())
	d := f.mustCall(t, "GET", "/api/admin/files?lang="+testLang+"&q=Etc", "")
	if d["total"] != float64(1) {
		t.Fatalf("按路径过滤错: %v", d)
	}
	row := d["files"].([]any)[0].(map[string]any)
	if row["ids"] != float64(1) || row["status"] != "ok" {
		t.Errorf("文件行内容错: %v", row)
	}
	// 路径里的 % 是字面量，不是通配符。
	if d = f.mustCall(t, "GET", "/api/admin/files?lang="+testLang+"&q=04%2530", ""); d["total"] != float64(0) {
		t.Errorf("LIKE 通配符未转义: %v", d)
	}
	d = f.mustCall(t, "GET", "/api/admin/runs?lang="+testLang, "")
	runs, _ := d["runs"].([]any)
	if len(runs) == 0 {
		t.Fatalf("扫描历史为空: %v", d)
	}
	first := runs[0].(map[string]any)
	if first["parsed"] == nil || first["startedAt"] == "" {
		t.Errorf("历史内容错: %v", first)
	}
}

func TestAdminItemEditAndDelete(t *testing.T) {
	f := newAdminServer(t, withToken())
	out := f.mustCall(t, "POST", "/api/admin/item?lang="+testLang,
		`{"id":"02000000","name":"超级药水","info":{"slotMax":"999"},"edited":true}`)
	if out["name"] != "超级药水" || out["edited"] != true {
		t.Fatalf("保存回读错: %v", out)
	}
	if out["icon"] != "/img/02000000.png" {
		t.Errorf("管理端返回也应带图标: %v", out)
	}
	if info := out["info"].(map[string]any); info["slotMax"] != "999" {
		t.Errorf("info 未写入: %v", info)
	}
	if _, err := f.db.UpdateItem(store.KindItem, testLang, "04000000", store.ItemEdit{Name: "手工", Edited: true}); err != nil {
		t.Fatal(err)
	}
	// 手工行默认受保护：全库重载也不覆盖。
	f.mustCall(t, "POST", "/api/admin/reload?lang="+testLang, `{"all":true}`)
	if job := f.waitJob(t); job["state"] != "done" {
		t.Fatalf("全库重载失败: %v", job)
	}
	if it, _, _ := f.db.Get(store.KindItem, testLang, "04000000"); it.Name != "手工" {
		t.Errorf("手工行被重载覆盖: %+v", it)
	}
	if s := f.waitJob(t); s["state"] != "done" {
		t.Fatalf("第二次重载失败: %v", s)
	}
	// 明确要求覆盖时，XML 的值赢。
	f.mustCall(t, "POST", "/api/admin/reload?lang="+testLang, `{"all":true,"overwriteEdited":true}`)
	f.waitJob(t)
	if it, _, _ := f.db.Get(store.KindItem, testLang, "04000000"); it.Name != "蓝色蜗牛壳" {
		t.Errorf("overwriteEdited 未生效: %+v", it)
	}

	if code, _, _ := f.call(t, "DELETE", "/api/admin/item?lang="+testLang+"&id=02000000", "", adminToken); code != 200 {
		t.Fatalf("删除应 200，实际 %d", code)
	}
	if code, _, _ := f.call(t, "GET", "/api/item?lang="+testLang+"&id=02000000", "", ""); code != 404 {
		t.Errorf("删除后应 404，实际 %d", code)
	}
	if code, _, _ := f.call(t, "POST", "/api/admin/item?lang="+testLang, `{"id":"99999999","name":"x"}`, adminToken); code != 404 {
		t.Errorf("改不存在的行应 404，实际 %d", code)
	}
}

func TestAdminSourcesAndPrune(t *testing.T) {
	f := newAdminServer(t, withToken())
	d := f.mustCall(t, "GET", "/api/admin/sources?lang="+testLang+"&id=04000000", "")
	if paths, _ := d["paths"].([]any); len(paths) != 2 {
		t.Fatalf("应反查到名称侧与属性侧两个文件: %v", paths)
	}
	if err := os.Remove(filepath.Join(f.root, "String.wz", "Consume.img.xml")); err != nil {
		t.Fatal(err)
	}
	d = f.mustCall(t, "GET", "/api/admin/diff?lang="+testLang, "")
	if d["gone"] != float64(1) {
		t.Fatalf("应检出 1 个已删除: %v", d)
	}
	f.mustCall(t, "POST", "/api/admin/prune?lang="+testLang, `{}`)
	if job := f.waitJob(t); job["state"] != "done" {
		t.Fatalf("清理任务失败: %v", job)
	}
	// 只由被删文件贡献的名称应清掉；另一侧文件的属性不受影响。
	if it, _, err := f.db.Get(store.KindItem, testLang, "02000000"); err != nil || it.HasName {
		t.Errorf("已删文件的独占贡献未清除: %+v err=%v", it, err)
	}
	if it, _, err := f.db.Get(store.KindItem, testLang, "04000000"); err != nil || it.Info["price"] != "1" {
		t.Errorf("仍在文件的属性被误清: %+v err=%v", it, err)
	}
	// 没有可清理项时应报 400，而不是静默成功。
	if code, _, _ := f.call(t, "POST", "/api/admin/prune?lang="+testLang, `{}`, adminToken); code != http.StatusBadRequest {
		t.Errorf("无差异应 400，实际 %d", code)
	}
}

func TestAdminGuard(t *testing.T) {
	f := newAdminServer(t, withToken())
	if code, _, _ := f.call(t, "GET", "/api/admin/diff?lang="+testLang, "", ""); code != http.StatusUnauthorized {
		t.Errorf("缺口令应 401，实际 %d", code)
	}
	if code, _, _ := f.call(t, "GET", "/api/admin/diff?lang="+testLang, "", "错的"); code != http.StatusUnauthorized {
		t.Errorf("错口令应 401，实际 %d", code)
	}
	if code, _, _ := f.call(t, "POST", "/api/admin/reload?lang="+testLang, `{"all":true}`, adminToken); code != http.StatusAccepted {
		t.Errorf("带口令应 202，实际 %d", code)
	}
	// 普通检索接口不受闸门影响。
	if code, _, _ := f.call(t, "GET", "/api/items?lang="+testLang, "", ""); code != 200 {
		t.Errorf("只读接口不该要口令，实际 %d", code)
	}
}

func TestAdminDisabled(t *testing.T) {
	off := false
	f := newAdminServer(t, config.Admin{Enabled: &off})
	if code, _, _ := f.call(t, "GET", "/api/admin/diff?lang="+testLang, "", adminToken); code != http.StatusNotFound {
		t.Errorf("关闭后管理接口应 404，实际 %d", code)
	}
	resp, err := http.Get(f.srv.URL + "/admin")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("关闭后管理页应 404，实际 %d", resp.StatusCode)
	}
}

func TestAdminPageServed(t *testing.T) {
	f := newAdminServer(t, withToken())
	resp, err := http.Get(f.srv.URL + "/admin")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(raw), "WzItemArchive 数据管理") {
		t.Errorf("管理页应内嵌可访问: %d len=%d", resp.StatusCode, len(raw))
	}
	for _, want := range []string{"/api/admin/reload", "重载新增+变更", "同时覆盖手工修改过的行"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("管理页缺少入口 %q", want)
		}
	}
}
