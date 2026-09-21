package scan

import (
	"github.com/sqx6781268/MapleWzMeta/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeXML 在假造的 WZ 根下按相对路径落一个文件。
func writeXML(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const stringItemXML = `<?xml version="1.0" encoding="UTF-8"?>
<imgdir name="String.wz/Item.img">
  <imgdir name="4000000">
    <string name="name" value="蓝色蜗牛壳"/>
    <string name="desc" value="蜗牛掉落的东西"/>
  </imgdir>
  <imgdir name="2000000">
    <string name="name" value="红色药水"/>
  </imgdir>
</imgdir>`

const itemEtcXML = `<?xml version="1.0" encoding="UTF-8"?>
<imgdir name="0430.img">
  <imgdir name="4000000">
    <imgdir name="info">
      <int name="slotMax" value="100"/>
      <int name="price" value="1"/>
    </imgdir>
    <canvas name="icon" width="30" height="30"/>
  </imgdir>
  <imgdir name="2000000">
    <imgdir name="info">
      <int name="slotMax" value="100"/>
    </imgdir>
  </imgdir>
</imgdir>`

func TestRunParsesAssociatesAndPersists(t *testing.T) {
	root := t.TempDir()
	writeXML(t, root, "String.wz/Item.img.xml", stringItemXML)
	writeXML(t, root, "Item.wz/Etc/0430.img.xml", itemEtcXML)

	db := openDB(t)
	res, err := Run(Options{Lang: "zh-CN", Root: root, Sources: []string{"String.wz", "Item.wz"}, Quiet: true}, db)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Found != 2 || res.Parsed != 2 || res.Failed != 0 {
		t.Errorf("文件统计错: %+v", res)
	}
	if res.Names != 2 || res.Infos != 2 {
		t.Errorf("条目统计错: names=%d infos=%d", res.Names, res.Infos)
	}

	it, ok, err := db.Get("zh-CN", "04000000")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if it.Name != "蓝色蜗牛壳" || it.Descr != "蜗牛掉落的东西" {
		t.Errorf("名称未关联上: %+v", it)
	}
	if it.Info["slotMax"] != "100" || it.Category != "Etc" {
		t.Errorf("属性未关联上: %+v", it)
	}
	if !it.HasName || !it.HasInfo {
		t.Errorf("完整度标记错: %+v", it)
	}
	// 2000000 在 String.wz 与 Item.wz 两侧都存在，应该都补齐。
	if red, ok, _ := db.Get("zh-CN", "02000000"); !ok || red.Name != "红色药水" || red.Info["slotMax"] != "100" {
		t.Errorf("02000000 关联错: %+v", red)
	}
}

func TestRunIsIncrementalByFingerprint(t *testing.T) {
	root := t.TempDir()
	writeXML(t, root, "String.wz/Item.img.xml", stringItemXML)
	writeXML(t, root, "Item.wz/Etc/0430.img.xml", itemEtcXML)
	db := openDB(t)

	if _, err := Run(Options{Lang: "zh-CN", Root: root, Quiet: true}, db); err != nil {
		t.Fatal(err)
	}
	// 原样重跑：全部按指纹跳过，不再解析。
	again, err := Run(Options{Lang: "zh-CN", Root: root, Quiet: true}, db)
	if err != nil {
		t.Fatal(err)
	}
	if again.Parsed != 0 || again.Skipped != 2 {
		t.Errorf("增量为生效: %+v", again)
	}

	// 改动内容（字节数变了）后应重新解析该文件，另一个仍跳过。
	p := filepath.Join(root, "Item.wz", "Etc", "0430.img.xml")
	raw, _ := os.ReadFile(p)
	writeXML(t, root, "Item.wz/Etc/0430.img.xml", string(raw)+"<!--改-->")
	third, err := Run(Options{Lang: "zh-CN", Root: root, Quiet: true}, db)
	if err != nil {
		t.Fatal(err)
	}
	if third.Parsed != 1 || third.Skipped != 1 {
		t.Errorf("变更后应只重解析 1 个: %+v", third)
	}
}

func TestRunRecordsFailedFiles(t *testing.T) {
	root := t.TempDir()
	writeXML(t, root, "Item.wz/Etc/0430.img.xml", `这不是 XML，只是一段中文说明。`)
	db := openDB(t)
	res, err := Run(Options{Lang: "zh-CN", Root: root, Quiet: true}, db)
	if err != nil {
		t.Fatalf("Run 不应因单文件失败而整体报错: %v", err)
	}
	if res.Failed != 1 || res.Parsed != 0 {
		t.Errorf("失败统计错: %+v", res)
	}
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "0430.img.xml") {
		t.Errorf("失败样例未记录: %v", res.Errors)
	}
	st, err := db.Stats("zh-CN")
	if err != nil || st.Failed != 1 {
		t.Errorf("失败文件应入库: stats=%+v err=%v", st, err)
	}
}

func openDB(t *testing.T) *store.DB {
	t.Helper()
	d, err := store.Open(filepath.Join(t.TempDir(), "scan.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

const stringItemXMLEn = `<?xml version="1.0" encoding="UTF-8"?>
<imgdir name="String.wz/Item.img">
  <imgdir name="4000000">
    <string name="name" value="Blue Snail Shell"/>
  </imgdir>
</imgdir>`

// 两套语言包各自入库，同 ID 不互相覆盖。
func TestRunSeparatesLocales(t *testing.T) {
	zhRoot := t.TempDir()
	writeXML(t, zhRoot, "String.wz/Item.img.xml", stringItemXML)
	writeXML(t, zhRoot, "Item.wz/Etc/0430.img.xml", itemEtcXML)
	enRoot := t.TempDir()
	writeXML(t, enRoot, "String.wz/Item.img.xml", stringItemXMLEn)

	db := openDB(t)
	if _, err := Run(Options{Lang: "zh-CN", Root: zhRoot, Quiet: true}, db); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Options{Lang: "en", Root: enRoot, Quiet: true}, db); err != nil {
		t.Fatal(err)
	}

	zh, ok, err := db.Get("zh-CN", "04000000")
	if err != nil || !ok || zh.Name != "蓝色蜗牛壳" || !zh.HasInfo {
		t.Errorf("zh-CN 域错: %+v ok=%v err=%v", zh, ok, err)
	}
	en, ok, err := db.Get("en", "04000000")
	if err != nil || !ok || en.Name != "Blue Snail Shell" {
		t.Errorf("en 域错: %+v ok=%v err=%v", en, ok, err)
	}
	if en.HasInfo {
		t.Errorf("en 域不该看到 zh-CN 的属性: %+v", en.Info)
	}
	// 检索也必须限定语言域。
	rows, total, err := db.Search(store.Query{Lang: "en", Name: "Snail"})
	if err != nil || total != 1 || rows[0].ID != "04000000" {
		t.Errorf("en 检索错: total=%d err=%v", total, err)
	}
	langs, err := db.Langs()
	if err != nil || len(langs) != 2 {
		t.Errorf("Langs 应列出两套域: %+v err=%v", langs, err)
	}
}
