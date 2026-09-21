// Command maplewzmeta 是 WzItemArchive 的唯一入口：扫描入库、命令行检索、起查询网站。
//
//	maplewzmeta scan   -config wzconfig.json              # 按配置里的 locales 逐个扫描（增量）
//	maplewzmeta scan   -config wzconfig.json -lang zh-CN  # 只扫一个语言
//	maplewzmeta query  -q 药水                            # 命令行检索
//	maplewzmeta stats                                     # 库内概况
//	maplewzmeta serve                                     # 打开内置查询页
//
// 所有路径（wz 目录、SQLite、图标目录）都来自 wzconfig.json，命令行只保留筛选与覆盖项。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sqx6781268/MapleWzMeta/internal/config"
	"github.com/sqx6781268/MapleWzMeta/internal/icons"
	"github.com/sqx6781268/MapleWzMeta/internal/scan"
	"github.com/sqx6781268/MapleWzMeta/internal/store"
	"github.com/sqx6781268/MapleWzMeta/internal/web"
)

const usage = `用法: maplewzmeta <子命令> [参数]

  scan    扫描 WZ 导出目录，解析并按物品 ID 关联后写入 SQLite（按 size+mtime 增量）
  query   在命令行检索已入库的物品
  stats   打印库内概况
  serve   启动 HTTP 服务，浏览器打开内置查询页

配置统一取自 wzconfig.json（默认找当前目录，-config 指定）：
  db / addr / workers / icons / locales[]，一个 locale = 一套语言的 wz 导出目录。

用 maplewzmeta <子命令> -h 查看各子命令参数。`

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		fmt.Println(usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	var err error
	switch cmd {
	case "scan":
		err = runScan(args)
	case "query":
		err = runQuery(args)
	case "stats":
		err = runStats(args)
	case "serve":
		err = runServe(args)
	case "-h", "--help", "help":
		fmt.Println(usage)
	default:
		fmt.Println(usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("%s 失败: %v", cmd, err)
	}
}

// openDB 按配置打开库；dbPath 非空时覆盖配置值。
func openDB(cfg config.Config, dbPath string) (*store.DB, error) {
	if dbPath != "" {
		cfg.DB = dbPath
	}
	return store.Open(cfg.DB)
}

func runScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	configPath := fs.String("config", "", "配置文件路径，默认找 ./wzconfig.json")
	langs := fs.String("lang", "all", "只扫描这些语言，逗号分隔；all 表示配置里的全部")
	limit := fs.Int("limit", 0, "每个语言最多解析多少个待处理文件，0 表示不限（调试用）")
	batch := fs.Int("batch", 400, "每攒多少文件写一次库")
	dbPath := fs.String("db", "", "覆盖配置里的 SQLite 路径")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	picked, err := cfg.Picked(*langs)
	if err != nil {
		return err
	}
	db, err := openDB(cfg, *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	for _, l := range picked {
		log.Printf("[%s] 扫描 %s（源：%s）", l.Lang, l.Wz, strings.Join(l.Sources, "、"))
		res, err := scan.Run(scan.Options{
			Lang: l.Lang, Root: l.Wz, Sources: l.Sources,
			Workers: cfg.Workers, Limit: *limit, Batch: *batch,
		}, db)
		if err != nil {
			return fmt.Errorf("%s: %w", l.Lang, err)
		}
		fmt.Printf("\n=== [%s] 扫描完成（用时 %s）===\n", l.Lang, res.Duration.Truncate(time.Millisecond))
		fmt.Printf("磁盘文件 %d，跳过未变 %d，解析成功 %d，失败 %d\n", res.Found, res.Skipped, res.Parsed, res.Failed)
		fmt.Printf("写入名称条目 %d，属性条目 %d；uol 展开 %d，容错修复 %d 处\n",
			res.Names, res.Infos, res.UolFixed, res.Repaired)
		for _, e := range res.Errors {
			fmt.Printf("  失败: %s\n", e)
		}
		if err := printStats(db, cfg, l.Lang); err != nil {
			return err
		}
	}
	return nil
}

func runQuery(args []string) error {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	configPath := fs.String("config", "", "配置文件路径，默认找 ./wzconfig.json")
	lang := fs.String("lang", "", "语言标识，缺省用配置里的第一个 locale")
	name := fs.String("q", "", "名称模糊匹配")
	id := fs.String("id", "", "精确 ID")
	prefix := fs.String("prefix", "", "ID 前缀")
	cat := fs.String("cat", "", "分类")
	attr := fs.String("attr", "", "属性等值过滤，形如 cash=1")
	complete := fs.Bool("complete", false, "只要名称与属性齐全")
	unnamed := fs.Bool("unnamed", false, "只要缺名称的（查导出缺口）")
	limit := fs.Int("limit", 30, "返回条数")
	jsonOut := fs.Bool("json", false, "以 JSON 输出")
	dbPath := fs.String("db", "", "覆盖配置里的 SQLite 路径")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	l, err := wantLang(cfg, *lang)
	if err != nil {
		return err
	}
	db, err := openDB(cfg, *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	q := store.Query{
		Lang: l.Lang, Name: *name, ID: *id, IDPrefix: *prefix, Category: *cat,
		Limit: *limit, OrderBy: "id",
	}
	if *attr != "" {
		q.Wants = []string{*attr}
	}
	if *complete {
		t := true
		q.HasName, q.HasInfo = &t, &t
	}
	if *unnamed {
		f, t := false, true
		q.HasName, q.HasInfo = &f, &t
	}
	rows, total, err := db.Search(q)
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		return enc.Encode(map[string]any{"lang": l.Lang, "total": total, "items": rows})
	}
	fmt.Printf("语言 %s：命中 %d 条，显示前 %d 条\n", l.Lang, total, len(rows))
	for _, it := range rows {
		fmt.Printf("  %s  %-16s %-12s %s\n", it.ID, it.Name, it.Category, pick(it.Info))
	}
	return nil
}

func runStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	configPath := fs.String("config", "", "配置文件路径，默认找 ./wzconfig.json")
	langs := fs.String("lang", "all", "打印哪些语言的概况，逗号分隔")
	dbPath := fs.String("db", "", "覆盖配置里的 SQLite 路径")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	picked, err := cfg.Picked(*langs)
	if err != nil {
		return err
	}
	db, err := openDB(cfg, *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Printf("\n=== 库内概况 %s ===\n", cfg.DB)
	if cfg.Icons.Enabled {
		ix, err := icons.Build(cfg.Icons.Dir)
		if err != nil {
			return err
		}
		ix.WarmLog()
	} else {
		fmt.Println("图标：未启用（icons.enabled=false）")
	}
	for _, l := range picked {
		if err := printStats(db, cfg, l.Lang); err != nil {
			return err
		}
	}
	return nil
}

// printStats 打印某个语言域的规模概况。
func printStats(db *store.DB, cfg config.Config, lang string) error {
	st, err := db.Stats(lang)
	if err != nil {
		return err
	}
	pct := func(n int) float64 {
		if st.Items == 0 {
			return 0
		}
		return float64(n) * 100 / float64(st.Items)
	}
	label := lang
	if l, ok := cfg.Locale(lang); ok && l.Label != "" {
		label = l.Label
	}
	fmt.Printf("\n--- %s（%s）---\n", lang, label)
	fmt.Printf("物品 %d 条：有名称 %d (%.2f%%)，有属性 %d (%.2f%%)，齐全 %d (%.2f%%)\n",
		st.Items, st.Named, pct(st.Named), st.WithInfo, pct(st.WithInfo), st.Complete, pct(st.Complete))
	fmt.Printf("已记录文件 %d 个，其中解析失败 %d 个\n", st.Files, st.Failed)
	if len(st.Categories) > 0 {
		var parts []string
		for _, c := range st.Categories {
			parts = append(parts, fmt.Sprintf("%s=%d", c.Name, c.Count))
		}
		fmt.Printf("分类 Top%d：%s\n", len(st.Categories), strings.Join(parts, "  "))
	}
	return nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "", "配置文件路径，默认找 ./wzconfig.json")
	addr := fs.String("addr", "", "监听地址，缺省用配置里的 addr")
	dbPath := fs.String("db", "", "覆盖配置里的 SQLite 路径")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	db, err := openDB(cfg, *dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	var ix *icons.Index
	if cfg.Icons.Enabled {
		ix, err = icons.Build(cfg.Icons.Dir)
		if err != nil {
			return err
		}
		ix.WarmLog()
	} else {
		log.Printf("图标资源未启用（icons.enabled=false），页面不显示图标列")
	}
	if *addr != "" {
		cfg.Addr = *addr
	}
	return web.New(db, cfg, ix).Serve(cfg.Addr)
}

// wantLang 校验命令行指定的语言是否在配置的 locales 内。
func wantLang(cfg config.Config, lang string) (config.Locale, error) {
	if strings.TrimSpace(lang) == "" {
		l, ok := cfg.Locale(cfg.DefaultLang())
		if !ok {
			return l, fmt.Errorf("config: 未配置任何 locale")
		}
		return l, nil
	}
	l, ok := cfg.Locale(lang)
	if !ok {
		return l, fmt.Errorf("config: 未配置的语言 %q，可选：%s", lang, strings.Join(cfg.Langs(), ", "))
	}
	return l, nil
}

// pick 挑几个关键属性用于命令行速览。
func pick(m map[string]string) string {
	prefer := []string{"reqLevel", "islot", "slotMax", "price", "cash", "attack", "incPAD", "incMMP", "undead"}
	var parts []string
	for _, k := range prefer {
		if v, ok := m[k]; ok {
			parts = append(parts, k+"="+v)
		}
	}
	if len(parts) == 0 {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(parts) >= 4 {
				break
			}
			parts = append(parts, k+"="+m[k])
		}
	}
	return strings.Join(parts, " ")
}
