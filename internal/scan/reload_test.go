package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sqx6781268/MapleWzMeta/internal/store"
)

// 属性侧文件被改小：0430.img.xml 只剩 4000000，且 price 这个键没了。
const itemEtcXMLSlim = `<?xml version="1.0" encoding="UTF-8"?>
<imgdir name="0430.img">
  <imgdir name="4000000">
    <imgdir name="info">
      <int name="slotMax" value="999"/>
    </imgdir>
  </imgdir>
</imgdir>`

// 名称侧文件被改小：蓝色蜗牛壳这个 ID 从 String.wz 里没了。
const stringItemXMLSlim = `<?xml version="1.0" encoding="UTF-8"?>
<imgdir name="String.wz/Etc.img">
  <imgdir name="2000000">
    <string name="name" value="红色药水"/>
  </imgdir>
</imgdir>`

func TestRunOverwriteReplacesAndClears(t *testing.T) {
	root := t.TempDir()
	writeXML(t, root, "String.wz/Etc.img.xml", stringItemXML)
	writeXML(t, root, "Item.wz/Etc/0430.img.xml", itemEtcXML)
	db := openDB(t)

	if _, err := Run(Options{Lang: "zh-CN", Root: root, Quiet: true}, db); err != nil {
		t.Fatal(err)
	}
	// 溯源要落库，否则后续覆盖无从判断。
	ids, err := db.FileIDs("zh-CN", "Item.wz/Etc/0430.img.xml")
	if err != nil || len(ids) != 2 {
		t.Fatalf("首次扫描应写入 2 个溯源 ID: %v err=%v", ids, err)
	}

	writeXML(t, root, "Item.wz/Etc/0430.img.xml", itemEtcXMLSlim)
	res, err := Run(Options{
		Lang: "zh-CN", Root: root, Quiet: true,
		Paths: []string{"Item.wz/Etc/0430.img.xml"}, Force: true, Overwrite: true, SkipEdited: true,
	}, db)
	if err != nil {
		t.Fatal(err)
	}
	if res.Parsed != 1 || res.Cleared != 1 {
		t.Errorf("应解析 1 个文件并清掉 1 条失效贡献: %+v", res)
	}
	kept, ok, err := db.Get(store.KindItem, "zh-CN", "04000000")
	if err != nil || !ok {
		t.Fatalf("查 04000000: ok=%v err=%v", ok, err)
	}
	if kept.Info["slotMax"] != "999" {
		t.Errorf("info 没整包替换: %+v", kept.Info)
	}
	if len(kept.Info) != 1 {
		t.Errorf("旧键没被替换掉: %+v", kept.Info)
	}
	if !kept.HasName || kept.Name != "蓝色蜗牛壳" {
		t.Errorf("名称侧不该动: %+v", kept)
	}
	gone, ok, err := db.Get(store.KindItem, "zh-CN", "02000000")
	if err != nil || !ok {
		t.Fatalf("查 02000000: ok=%v err=%v", ok, err)
	}
	if gone.HasInfo || len(gone.Info) != 0 {
		t.Errorf("该文件不再贡献 02000000，属性应清掉: %+v", gone)
	}
	if gone.Name != "红色药水" || !gone.HasName {
		t.Errorf("名称侧来自另一个文件，不该被属性侧重载清掉: %+v", gone)
	}

	// 名称侧重载：ID 从 XML 里消失后，名称一并清除。
	writeXML(t, root, "String.wz/Etc.img.xml", stringItemXMLSlim)
	res, err = Run(Options{
		Lang: "zh-CN", Root: root, Quiet: true,
		Paths: []string{"String.wz/"}, Force: true, Overwrite: true, SkipEdited: true,
	}, db)
	if err != nil {
		t.Fatal(err)
	}
	if res.Parsed != 1 || res.Cleared != 1 {
		t.Errorf("目录前缀选择符没生效: %+v missing=%v", res, res.Missing)
	}
	if it, ok, err := db.Get(store.KindItem, "zh-CN", "04000000"); err != nil || !ok || it.HasName {
		t.Errorf("名称未随 XML 删除而清除: %+v ok=%v err=%v", it, ok, err)
	}
}

func TestRunReloadKeepsManualEditsAndReportsMissing(t *testing.T) {
	root := t.TempDir()
	writeXML(t, root, "String.wz/Etc.img.xml", stringItemXML)
	writeXML(t, root, "Item.wz/Etc/0430.img.xml", itemEtcXML)
	db := openDB(t)
	if _, err := Run(Options{Lang: "zh-CN", Root: root, Quiet: true}, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateItem(store.KindItem, "zh-CN", "04000000", store.ItemEdit{
		Name: "蓝色蜗牛壳(手工)", Descr: "蜗牛壳", Category: "Etc", Edited: true,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := Run(Options{
		Lang: "zh-CN", Root: root, Quiet: true,
		Paths:     []string{"String.wz/Etc.img.xml", "String.wz/不存在.img.xml"},
		Force:     true,
		Overwrite: true, SkipEdited: true,
	}, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Missing) != 1 || !strings.Contains(res.Missing[0], "不存在") {
		t.Errorf("未命中的选择符要报出来: %v", res.Missing)
	}
	if res.Protected == 0 {
		t.Errorf("手工行应被跳过并计数: %+v", res)
	}
	if it, _, _ := db.Get(store.KindItem, "zh-CN", "04000000"); it.Name != "蓝色蜗牛壳(手工)" {
		t.Errorf("手工修改被覆盖了: %+v", it)
	}
	// 显式允许覆盖时，手工行也让位于 XML。
	if _, err := Run(Options{Lang: "zh-CN", Root: root, Quiet: true,
		Paths: []string{"String.wz/Etc.img.xml"}, Force: true, Overwrite: true}, db); err != nil {
		t.Fatal(err)
	}
	if it, _, _ := db.Get(store.KindItem, "zh-CN", "04000000"); it.Name != "蓝色蜗牛壳" {
		t.Errorf("允许覆盖时没生效: %+v", it)
	}
}

func TestCollectReturnsSortedFingerprints(t *testing.T) {
	root := t.TempDir()
	writeXML(t, root, "Item.wz/Etc/0430.img.xml", itemEtcXML)
	writeXML(t, root, "String.wz/Etc.img.xml", stringItemXML)
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("不是 xml"), 0o644); err != nil {
		t.Fatal(err)
	}
	recs, err := Collect(root, []string{"String.wz", "Item.wz"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].Path != "Item.wz/Etc/0430.img.xml" {
		t.Errorf("采集结果错: %+v", recs)
	}
	for _, r := range recs {
		if r.Size == 0 || r.ModTime.IsZero() {
			t.Errorf("指纹不全: %+v", r)
		}
	}
}
