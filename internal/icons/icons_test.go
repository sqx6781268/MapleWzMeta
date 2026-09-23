package icons

import (
	"archive/zip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	// Npc 的 7 位文件名补零后与物品 00002000 撞号，物品域应取装备图。
	writeFile(t, root, "Npc/0002000.img.png", "NPC")
	writeFile(t, root, "Character/Accessory/00002000.img.png", "配饰")
	writeFile(t, root, "Character/Cap/readme.txt", "非图片")
	// 只存在于别的实体域的图：物品行绝不能拿它们当图标（实测 1,578 行串味）。
	writeFile(t, root, "Npc/0009999.img.png", "只有NPC图")
	writeFile(t, root, "Mob/0000000.img.png", "怪")
	return root
}

func TestBuildIndexNormalizesIDs(t *testing.T) {
	x, err := Build(buildFakeData(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if x.Count() != 6 {
		t.Errorf("应索引 6 个 png，实际 %d", x.Count())
	}
	if x.IDs() != 5 {
		t.Errorf("去重后应 5 个 ID，实际 %d", x.IDs())
	}
	// 7 位与 8 位写法都要能用 8 位 ID 查到。
	if p, ok := x.Path("item", "05000000"); !ok || filepath.Base(p) != "5000000.img.png" {
		t.Errorf("7 位文件名未对齐 8 位 ID: %s ok=%v", p, ok)
	}
	if !x.Has("item", "5000000") {
		t.Errorf("原始 7 位 ID 也应命中")
	}
}

func TestItemIconsBeatNpcOnCollision(t *testing.T) {
	x, err := Build(buildFakeData(t))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := x.Path("item", "00002000")
	if !ok {
		t.Fatal("00002000 未索引到")
	}
	if filepath.Base(filepath.Dir(p)) != "Accessory" {
		t.Errorf("同 ID 冲突时物品域应取 Character/Item，实际 %s", p)
	}
	// 只有 Npc / Mob 域有图的 ID，物品域必须查不到——这正是旧版把 NPC 立绘挂到物品行的缺陷。
	if x.Has("item", "00009999") {
		t.Errorf("物品域不该取到只存在于 Npc 域的图")
	}
	if !x.Has("npc", "9999") {
		t.Errorf("NPC 域应能取到自己目录的图")
	}
	if x.Has("item", "0") || !x.Has("mob", "0000000") {
		t.Errorf("怪物域应只认 Mob 目录：item=%v mob=%v", x.Has("item", "0"), x.Has("mob", "0000000"))
	}
	// 未知实体域退回全域最优，保证老调用方不因传了陌生值就拿不到图。
	if !x.Has("whatever", "00009999") {
		t.Errorf("未知域应退回全域索引")
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

	// ?for= 决定取哪个域：9999 只有 Npc 域有图，缺省（物品域）应 404。
	for _, u := range []string{"/img/00009999.png", "/img/00009999.png?for=item"} {
		r, err := http.Get(srv.URL + u)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("%s 物品域不该有图，实际 %d", u, r.StatusCode)
		}
	}
	r, err := http.Get(srv.URL + "/img/00009999.png?for=npc")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Errorf("NPC 域应取到自己目录的图，实际 %d", r.StatusCode)
	}
}

// writeZip 按给定压缩方式把 "包内路径 -> 内容" 打成一个 zip，返回文件路径。
func writeZip(t *testing.T, name string, method uint16, files [][2]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, kv := range files {
		hdr := &zip.FileHeader{Name: kv[0], Method: method}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(kv[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// Store 模式的 zip：启动只解析中央目录，读取按偏移量零拷贝取原始字节。
// 域、ID 归一、同 ID 优先级这些规则必须和目录模式完全一致。
func TestBuildFromStoreZip(t *testing.T) {
	p := writeZip(t, "imgdata.zip", zip.Store, [][2]string{
		{"Character/Cap/01002000.img.png", "帽"},
		{"Item/Pet/5000000.img.png", "宠物"},
		{"Npc/0002000.img.png", "NPC立绘"},
		{"Character/Cap/readme.txt", "非图片"},
	})
	x, err := Build(p)
	if err != nil {
		t.Fatalf("Build(zip): %v", err)
	}
	defer x.Close()

	if x.Count() != 3 {
		t.Errorf("应索引 3 张图，实际 %d", x.Count())
	}
	// 7 位文件名要能对上 8 位 ID；同 ID 的 NPC 立绘不进物品域。
	if !x.Has("npc", "2000") || !x.Has("npc", "00002000") {
		t.Errorf("NPC 域 ID 归一异常：2000=%v 00002000=%v", x.Has("npc", "2000"), x.Has("npc", "00002000"))
	}
	if !x.Has("item", "01002000") {
		t.Errorf("物品域应取到 Character/Cap 的图")
	}
	srv := httptest.NewServer(http.HandlerFunc(x.Serve))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/img/01002000.png")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("zip 取图应 200，实际 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("zip 取图应带 image/png，实际 %q", ct)
	}
	buf := make([]byte, 8)
	n, _ := resp.Body.Read(buf)
	if string(buf[:n]) != "帽" {
		t.Errorf("零拷贝读到的内容错: %q", buf[:n])
	}
}

// 打包时多套一层目录（`Compress-Archive imgdata imgdata.zip` 会得到 imgdata/Character/...），
// 不剥掉的话每张图都会被当成一个独立域。反斜杠条目名同理要归一。
func TestBuildFromZipWithWrappedRoot(t *testing.T) {
	p := writeZip(t, "imgdata.zip", zip.Store, [][2]string{
		{"imgdata/Character/Cap/01002000.img.png", "帽"},
		{`imgdata\Npc\0002000.img.png`, "NPC"},
	})
	x, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	if x.Count() != 2 {
		t.Fatalf("应索引 2 张，实际 %d", x.Count())
	}
	if !x.Has("item", "01002000") {
		t.Errorf("多套一层目录后物品域取不到图，域统计=%v", x.counts)
	}
	if !x.Has("npc", "0002000") {
		t.Errorf("反斜杠条目名没被归一，取不到 NPC 图")
	}
}

// deflate 打包的归档拿不到"压缩后=原始"的对应关系，必须回退到正常解压，不能返回脏字节。
func TestBuildFromDeflateZipFallsBack(t *testing.T) {
	body := strings.Repeat("mis-像素-", 200) // 有重复内容，deflate 后明显小于原始
	p := writeZip(t, "imgdata.zip", zip.Deflate, [][2]string{
		{"Item/Pet/5000000.img.png", body},
	})
	x, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	var sb strings.Builder
	ok, err := x.CopyTo(&sb, "item", "05000000")
	if err != nil || !ok {
		t.Fatalf("CopyTo: ok=%v err=%v", ok, err)
	}
	if sb.String() != body {
		t.Errorf("deflate 回退解压结果错：期望 %d 字节，实际 %d", len(body), sb.Len())
	}
}

// 目录模式与 zip 模式必须给出同样的取图结果——两条路共用一套索引与规则。
func TestZipAndDirIndexesAgree(t *testing.T) {
	dir := buildFakeData(t)
	p := writeZip(t, "imgdata.zip", zip.Store, [][2]string{
		{"Character/Cap/01002000.img.png", "帽"},
		{"Item/Pet/5000000.img.png", "宠物"},
		{"Npc/0002000.img.png", "NPC"},
		{"Character/Accessory/00002000.img.png", "配饰"},
		{"Npc/0009999.img.png", "只有NPC图"},
		{"Mob/0000000.img.png", "怪"},
	})
	dx, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	zx, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zx.Close()
	if dx.IDs() != zx.IDs() || dx.Count() != zx.Count() {
		t.Errorf("两种来源规模不一致 dir=%d/%d zip=%d/%d", dx.Count(), dx.IDs(), zx.Count(), zx.IDs())
	}
	for _, c := range []struct{ entity, id string }{
		{"item", "01002000"}, {"item", "00002000"}, {"item", "00009999"},
		{"npc", "00009999"}, {"mob", "0"}, {"npc", "01002000"},
	} {
		if dx.Has(c.entity, c.id) != zx.Has(c.entity, c.id) {
			t.Errorf("%s/%s 命中情况不一致 dir=%v zip=%v", c.entity, c.id, dx.Has(c.entity, c.id), zx.Has(c.entity, c.id))
		}
	}
}
