package wzxml

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 真实样例：Sound.wz/Bgm03.img.xml 里导出器写出了缺少 value 与 `/>` 的 <sound> 行。
func TestRobustRepairsTruncatedSoundTags(t *testing.T) {
	root, info, err := DecodeFileRobust(testdata(t, "broken_sound.img.xml"))
	if err != nil {
		t.Fatalf("容错解码失败: %v", err)
	}
	if info.RepairedTags != 3 {
		t.Errorf("RepairedTags = %d，期望 3", info.RepairedTags)
	}
	if root.Name != "Bgm03.img" {
		t.Errorf("根 name = %q", root.Name)
	}
	for _, name := range []string{"Subway", "Elfwood", "BlueSky"} {
		n := root.Child(name)
		if n == nil || n.Type != "sound" {
			t.Errorf("sound 节点 %s 缺失", name)
			continue
		}
	}
	if n := root.Child("Beached"); n == nil || n.Value != "res/Sound/Bgm/Beached.img" {
		t.Errorf("正常标签被改坏: %+v", n)
	}
}

func TestRobustGB18030ASCIIOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ascii_gb.img.xml")
	src := "<?xml version=\"1.0\" encoding=\"gb18030\"?>\r\n<imgdir name=\"0524.img\">\r\n  <imgdir name=\"05240000\">\r\n    <int name=\"price\" value=\"0\"/>\r\n  </imgdir>\r\n</imgdir>\r\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	root, info, err := DecodeFileRobust(p)
	if err != nil {
		t.Fatalf("纯 ASCII 的 gb18030 声明应可读取: %v", err)
	}
	if !info.NormalizedEncoding || info.DeclaredEncoding != "gb18030" {
		t.Errorf("RepairInfo = %+v", info)
	}
	if root.Find("05240000") == nil {
		t.Error("子节点丢失")
	}
}

// 声明 gb18030 且真的是多字节中文时，必须显式报错而不是产出乱码树。
func TestRobustRejectsRealGB18030(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "real_gb.img.xml")
	gbk := []byte{0xb6, 0xac, 0xd6, 0xc2} // "冬至" 的 GBK 编码
	src := "<?xml version=\"1.0\" encoding=\"gb18030\"?>\n<imgdir name=\"A\"><string name=\"name\" value=\""
	srcBytes := append([]byte(src), gbk...)
	srcBytes = append(srcBytes, []byte("\"/></imgdir>\n")...)
	if err := os.WriteFile(p, srcBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	_, info, err := DecodeFileRobust(p)
	if !errors.Is(err, ErrNonUTF8Content) {
		t.Fatalf("期望 ErrNonUTF8Content，实际 %v (info=%+v)", err, info)
	}
	if info.DeclaredEncoding != "gb18030" {
		t.Errorf("应记录声明编码，实际 %q", info.DeclaredEncoding)
	}
}

// 无法靠补自闭合符救回来的文件仍要报错，不能假装成功。
func TestRobustStillFailsOnHopelessInput(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.img.xml")
	if err := os.WriteFile(p, []byte("<imgdir name=\"A\"><int name=\"b\" value=\"1\"/></imgdi>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := DecodeFileRobust(p); err == nil {
		t.Fatal("期望报错")
	} else if !strings.Contains(err.Error(), "bad.img.xml") {
		t.Errorf("错误信息应含文件名: %v", err)
	}
}

func TestRepairDoesNotTouchValidLines(t *testing.T) {
	src := []byte("<imgdir name=\"A\"/>\n<string name=\"ok\" value=\"x\"/>\n</imgdir>\n<!-- 注释 -->\n")
	got, n := repairTruncatedTags(src)
	if n != 0 {
		t.Errorf("正常内容被计为修复 %d 处:\n%s", n, got)
	}
	if string(got) != string(src) {
		t.Errorf("内容被改动:\n%s", got)
	}
}
