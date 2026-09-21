// Package config 读取 wzconfig.json：WZ 目录、SQLite 路径、监听地址、图标可选配置，
// 以及多语言环境数组 locales（一个 locale = 一套语言包目录）。
//
// 只支持 JSON：本机不引 viper/yaml，标准库 encoding/json 足够，且配置里全是结构化字段。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DefaultFileName 是默认配置文件名。
const DefaultFileName = "wzconfig.json"

// DefaultSources 是参与物品关联的 .wz 源；其余包（Mob/Npc/Map…）属别的实体域。
var DefaultSources = []string{"String.wz", "Item.wz", "Character.wz"}

var langRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{1,19}$`)

// Icons 是图标资源配置。图标与语言无关，因此全局只配一份。
type Icons struct {
	// Enabled 为 false 时，索引与 /img 路由都不会注册，页面也不显示图标列。
	Enabled bool `json:"enabled"`
	// Dir 是导出器产出的图片根目录，实测布局为 <Dir>/<Wz>/<类别>/<id>.img.png。
	Dir string `json:"dir"`
}

// Locale 是一套语言环境。
type Locale struct {
	Lang    string   `json:"lang"`    // 语言标识，入库作为 item.lang
	Label   string   `json:"label"`   // 界面显示名
	Wz      string   `json:"wz"`      // 该语言的 WZ 导出根目录
	Sources []string `json:"sources"` // 参与关联的 .wz，留空用 DefaultSources
}

// Config 是运行期总配置。
type Config struct {
	DB      string   `json:"db"`
	Addr    string   `json:"addr"`
	Workers int      `json:"workers"`
	Icons   Icons    `json:"icons"`
	Locales []Locale `json:"locales"`

	// Path 是配置文件绝对路径；相对字段都以它所在目录为基准解析。
	Path string `json:"-"`
}

// Default 返回不依赖配置文件即可跑通的配置。
func Default() Config {
	return Config{
		DB:    "./data/mia.db",
		Addr:  "127.0.0.1:8080",
		Icons: Icons{Enabled: false, Dir: "./imgdata"},
		Locales: []Locale{{
			Lang: "zh-CN", Label: "简体中文", Wz: "./wz", Sources: DefaultSources,
		}},
	}
}

// Load 读取配置。path 为空时依次找 ./wzconfig.json；都不存在则返回 Default()。
// 配置文件里的相对路径按配置文件所在目录解析，避免受当前工作目录影响。
func Load(path string) (Config, error) {
	if path == "" {
		if _, err := os.Stat(DefaultFileName); err == nil {
			path = DefaultFileName
		}
	}
	if path == "" {
		cfg := Default()
		if err := cfg.resolve(); err != nil {
			return cfg, err
		}
		return cfg, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: 读取 %s 失败: %w", path, err)
	}
	// 允许 // 行注释，方便配置里写说明。
	raw = stripLineComments(raw)

	cfg := Config{}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: 解析 %s 失败: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	cfg.Path = abs
	cfg.mergeDefaults()
	if err := cfg.resolve(); err != nil {
		return cfg, err
	}
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c *Config) mergeDefaults() {
	d := Default()
	if c.DB == "" {
		c.DB = d.DB
	}
	if c.Addr == "" {
		c.Addr = d.Addr
	}
	if c.Icons.Dir == "" {
		c.Icons.Dir = d.Icons.Dir
	}
	if len(c.Locales) == 0 {
		c.Locales = d.Locales
	}
	for i := range c.Locales {
		l := &c.Locales[i]
		if len(l.Sources) == 0 {
			l.Sources = DefaultSources
		}
		if l.Label == "" {
			l.Label = l.Lang
		}
	}
}

// resolve 把相对路径改成绝对路径。必须是指针接收者，否则改写只落在副本上。
func (c *Config) resolve() error {
	base := "."
	if c.Path != "" {
		base = filepath.Dir(c.Path)
	}
	abs := func(p string) string {
		if p == "" {
			return ""
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		if a, err := filepath.Abs(p); err == nil {
			return filepath.Clean(a)
		}
		return filepath.Clean(p)
	}
	c.DB = abs(c.DB)
	c.Icons.Dir = abs(c.Icons.Dir)
	for i := range c.Locales {
		c.Locales[i].Wz = abs(c.Locales[i].Wz)
	}
	return nil
}

func (c Config) validate() error {
	seen := map[string]bool{}
	for i, l := range c.Locales {
		if !langRe.MatchString(l.Lang) {
			return fmt.Errorf("config: locales[%d].lang %q 不合法（2-20 位字母数字/_/-）", i, l.Lang)
		}
		if seen[l.Lang] {
			return fmt.Errorf("config: locales[%d].lang %q 重复", i, l.Lang)
		}
		seen[l.Lang] = true
		if l.Wz == "" {
			return fmt.Errorf("config: locales[%d].wz 不能为空", i)
		}
		if st, err := os.Stat(l.Wz); err != nil || !st.IsDir() {
			return fmt.Errorf("config: locales[%d]（%s）的 wz 目录不存在或不是目录: %s", i, l.Lang, l.Wz)
		}
	}
	if c.Icons.Enabled {
		st, err := os.Stat(c.Icons.Dir)
		if err != nil || !st.IsDir() {
			return fmt.Errorf("config: icons.enabled 为 true，但 icons.dir 不存在或不是目录: %s", c.Icons.Dir)
		}
	}
	return nil
}

// DefaultLang 是界面与 CLI 未指定语言时使用的语言。
func (c Config) DefaultLang() string {
	if len(c.Locales) == 0 {
		return ""
	}
	return c.Locales[0].Lang
}

// Langs 按配置顺序返回全部语言标识。
func (c Config) Langs() []string {
	out := make([]string, 0, len(c.Locales))
	for _, l := range c.Locales {
		out = append(out, l.Lang)
	}
	return out
}

// Locale 按语言标识取配置。
func (c Config) Locale(lang string) (Locale, bool) {
	for _, l := range c.Locales {
		if l.Lang == lang {
			return l, true
		}
	}
	return Locale{}, false
}

// Picked 按逗号分隔的语言清单取子集；空串表示全部。
func (c Config) Picked(list string) ([]Locale, error) {
	list = strings.TrimSpace(list)
	if list == "" || list == "all" {
		return c.Locales, nil
	}
	var out []Locale
	for _, lang := range strings.Split(list, ",") {
		lang = strings.TrimSpace(lang)
		if lang == "" {
			continue
		}
		l, ok := c.Locale(lang)
		if !ok {
			return nil, fmt.Errorf("config: 未配置的语言 %q，可选：%s", lang, strings.Join(c.Langs(), ", "))
		}
		out = append(out, l)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("config: -lang 未选中任何语言")
	}
	return out, nil
}

// stripLineComments 去掉整行的 // 注释，让配置里能写中文说明。
func stripLineComments(raw []byte) []byte {
	lines := strings.Split(string(raw), "\n")
	var kept []string
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		kept = append(kept, ln)
	}
	return []byte(strings.Join(kept, "\n"))
}
