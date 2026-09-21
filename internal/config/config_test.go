package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMissingFileFallsBackToDefault(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("无配置文件时应退回默认值: %v", err)
	}
	if cfg.DefaultLang() != "zh-CN" || len(cfg.Locales) != 1 {
		t.Errorf("默认语言错: %+v", cfg.Locales)
	}
	if cfg.Icons.Enabled {
		t.Errorf("默认应关闭图标")
	}
	if !filepath.IsAbs(cfg.DB) {
		t.Errorf("默认路径也应解析为绝对路径: %s", cfg.DB)
	}
}

func TestLoadResolvesRelativeToConfigDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "wz-en", "String.wz"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "imgdata"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := write(t, dir, "wzconfig.json", `{
  // 整行注释应被忽略
  "db": "data/mia.db",
  "icons": { "enabled": true, "dir": "imgdata" },
  "locales": [
    { "lang": "zh-CN", "wz": "wz-zh-CN" },
    { "lang": "en", "label": "English", "wz": "wz-en" }
  ]
}`)
	if err := os.MkdirAll(filepath.Join(dir, "wz-zh-CN"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DB != filepath.Join(dir, "data", "mia.db") {
		t.Errorf("db 未按配置目录解析: %s", cfg.DB)
	}
	if cfg.Icons.Dir != filepath.Join(dir, "imgdata") {
		t.Errorf("icons.dir 错: %s", cfg.Icons.Dir)
	}
	if got := cfg.Langs(); strings.Join(got, ",") != "zh-CN,en" {
		t.Errorf("语言顺序错: %v", got)
	}
	zh, _ := cfg.Locale("zh-CN")
	if strings.Join(zh.Sources, ",") != strings.Join(DefaultSources, ",") {
		t.Errorf("sources 留空应补默认值: %v", zh.Sources)
	}
	if zh.Label != "zh-CN" {
		t.Errorf("label 留空应回退为 lang: %s", zh.Label)
	}
	if cfg.DefaultLang() != "zh-CN" {
		t.Errorf("默认语言应取第一项")
	}
}

func TestValidateRejectsBadConfigs(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "wz"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, body, want string
	}{
		{"语言重复", `{"locales":[{"lang":"zh-CN","wz":"wz"},{"lang":"zh-CN","wz":"wz"}]}`, "重复"},
		{"语言标识非法", `{"locales":[{"lang":"中文","wz":"wz"}]}`, "不合法"},
		{"wz 目录缺失", `{"locales":[{"lang":"en","wz":"nope"}]}`, "wz 目录不存在"},
		{"图标目录缺失", `{"icons":{"enabled":true,"dir":"nope"},"locales":[{"lang":"zh-CN","wz":"wz"}]}`, "icons.dir"},
		{"未知字段", `{"locales":[{"lang":"zh-CN","wz":"wz"}],"locales2":[]}`, "未知字段|unknown"},
	}
	for _, c := range cases {
		p := write(t, base, "cfg.json", c.body)
		_, err := Load(p)
		if err == nil {
			t.Errorf("%s: 应报错但通过了", c.name)
			continue
		}
		ok := false
		for _, want := range strings.Split(c.want, "|") {
			if strings.Contains(err.Error(), want) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s: 报错信息不含期望关键字 %q，实际 %v", c.name, c.want, err)
		}
	}
}

func TestPicked(t *testing.T) {
	base := t.TempDir()
	for _, d := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := write(t, base, "cfg.json", `{"locales":[{"lang":"zh-CN","wz":"a"},{"lang":"en","wz":"b"}]}`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := cfg.Picked(""); err != nil || len(got) != 2 {
		t.Errorf("空清单应返回全部: %d %v", len(got), err)
	}
	got, err := cfg.Picked("en, zh-CN")
	if err != nil || len(got) != 2 || got[0].Lang != "en" {
		t.Errorf("按清单筛选错: %+v %v", got, err)
	}
	if _, err := cfg.Picked("ja"); err == nil || !strings.Contains(err.Error(), "未配置的语言") {
		t.Errorf("未配置语言应报错: %v", err)
	}
}
