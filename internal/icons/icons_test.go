package icons

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func buildFakeData(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "Character/Cap/01002000.img.png", "帽")
	writeFile(t, root, "Item/Pet/5000000.img.png", "宠物")
	// Npc 的 7 位文件名补零后与物品 00002000 撞号，装备侧应优先。
	writeFile(t, root, "Npc/0002000.img.png", "NPC")
	writeFile(t, root, "Character/Accessory/00002000.img.png", "配饰")
	writeFile(t, root, "Character/Cap/readme.txt", "非图片")
	writeFile(t, root, "Mob/0000000.img.png", "怪")
	return root
}

func TestBuildIndexNormalizesIDs(t *testing.T) {
	x, err := Build(buildFakeData(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if x.Count() != 5 {
		t.Errorf("应索引 5 个 png，实际 %d", x.Count())
	}
	if x.IDs() != 4 {
		t.Errorf("去重后应 4 个 ID，实际 %d", x.IDs())
	}
	// 7 位与 8 位写法都要能用 8 位 ID 查到。
	if p, ok := x.Path("05000000"); !ok || filepath.Base(p) != "5000000.img.png" {
		t.Errorf("7 位文件名未对齐 8 位 ID: %s ok=%v", p, ok)
	}
	if !x.Has("5000000") {
		t.Errorf("原始 7 位 ID 也应命中")
	}
}

func TestItemIconsBeatNpcOnCollision(t *testing.T) {
	x, err := Build(buildFakeData(t))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := x.Path("00002000")
	if !ok {
		t.Fatal("00002000 未索引到")
	}
	if filepath.Base(filepath.Dir(p)) != "Accessory" {
		t.Errorf("同 ID 冲突时应取 Character/Item，实际 %s", p)
	}
}

func TestBuildMissingDir(t *testing.T) {
	if _, err := Build(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Errorf("目录不存在应报错")
	}
}

func TestServe(t *testing.T) {
	x, err := Build(buildFakeData(t))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(x.Serve))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/img/01002000.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("已知 ID 应 200，实际 %d", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc == "" {
		t.Errorf("图标应带缓存头")
	}
	buf := make([]byte, 8)
	n, _ := resp.Body.Read(buf)
	if string(buf[:n]) != "帽" {
		t.Errorf("内容错: %q", buf[:n])
	}

	for _, u := range []string{"/img/99999999.png", "/img/../wzconfig.json", "/img/abc.png", "/img/01002000.txt"} {
		r, err := http.Get(srv.URL + u)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		r.Body.Close()
		// 越界与非法名必须被拒；未索引到的 ID 给 404。
		if r.StatusCode != http.StatusNotFound && r.StatusCode != http.StatusBadRequest {
			t.Errorf("%s 应 400/404，实际 %d", u, r.StatusCode)
		}
	}
}
