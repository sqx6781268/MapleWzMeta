// Command wzstats 全量扫描 WZ 导出的 img XML，解码并打平 uol，输出规模统计。
// 不写数据库，只用于验证解析内核在真实数据上的正确性与耗时基线。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sqx6781268/MapleWzMeta/internal/wzxml"
)

type fileStat struct {
	Path      string             `json:"path"`
	Size      int64              `json:"size"`
	ModTime   string             `json:"mod_time"`
	Nodes     int                `json:"nodes"`
	MaxDepth  int                `json:"max_depth"`
	Types     map[string]int     `json:"types,omitempty"`
	UolFound  int                `json:"uol_found"`
	UolUnres  int                `json:"uol_unresolved"`
	RootFixed int                `json:"uol_root_fallback"`
	Unres     []wzxml.Unresolved `json:"unresolved_sample,omitempty"`
	Repaired  int                `json:"repaired_tags,omitempty"`
	Encoding  string             `json:"declared_encoding,omitempty"`
	Err       string             `json:"err,omitempty"`
}

type bucket struct {
	Files        int            `json:"files"`
	Failed       int            `json:"failed"`
	Bytes        int64          `json:"bytes"`
	Nodes        int            `json:"nodes"`
	MaxDepth     int            `json:"max_depth"`
	Types        map[string]int `json:"types"`
	Uol          int            `json:"uol_found"`
	Unres        int            `json:"uol_unresolved"`
	RootFixed    int            `json:"uol_root_fallback"`
	UnresFiles   int            `json:"files_with_unresolved"`
	RepairedFile int            `json:"files_repaired"`
	RepairedTag  int            `json:"tags_repaired"`
	EncFixed     int            `json:"encoding_normalized"`
}

func newBucket() *bucket { return &bucket{Types: map[string]int{}} }

func (b *bucket) add(f fileStat) {
	b.Files++
	b.Bytes += f.Size
	b.Nodes += f.Nodes
	if f.MaxDepth > b.MaxDepth {
		b.MaxDepth = f.MaxDepth
	}
	b.Uol += f.UolFound
	b.Unres += f.UolUnres
	b.RootFixed += f.RootFixed
	if f.UolUnres > 0 {
		b.UnresFiles++
	}
	if f.Repaired > 0 {
		b.RepairedFile++
		b.RepairedTag += f.Repaired
	}
	if f.Encoding != "" && f.Encoding != "UTF-8" {
		b.EncFixed++
	}
	for t, c := range f.Types {
		b.Types[t] += c
	}
}

type summary struct {
	Root        string             `json:"root"`
	Workers     int                `json:"workers"`
	ElapsedMS   int64              `json:"elapsed_ms"`
	Total       bucket             `json:"total"`
	ByWz        map[string]*bucket `json:"by_wz"`
	ByCat       map[string]*bucket `json:"by_wz_category"`
	Errors      []fileStat         `json:"errors"`
	UnresSample []string           `json:"unresolved_sample"`
}

func main() {
	root := flag.String("root", filepath.Join(".", "wz"), "WZ 导出根目录")
	out := flag.String("out", "", "统计 JSON 输出路径，留空只打印摘要表")
	workers := flag.Int("workers", runtime.NumCPU(), "并发解析协程数")
	maxErr := flag.Int("max-errors", 200, "失败清单最多保留条数")
	flag.Parse()

	abs, err := filepath.Abs(*root)
	if err != nil {
		log.Fatalf("解析目录失败: %v", err)
	}
	files, err := collectXML(abs)
	if err != nil {
		log.Fatalf("遍历 %s 失败: %v", abs, err)
	}
	if len(files) == 0 {
		log.Fatalf("%s 下没有 XML 文件", abs)
	}
	log.Printf("发现 %d 个 XML 文件，并发 %d", len(files), *workers)

	start := time.Now()
	results := run(abs, files, *workers)

	sum := summary{Root: abs, Workers: *workers, Total: *newBucket(),
		ByWz: map[string]*bucket{}, ByCat: map[string]*bucket{}}
	var seen int64
	for f := range results {
		if f.Err != "" {
			sum.Total.Failed++
			if len(sum.Errors) < *maxErr {
				sum.Errors = append(sum.Errors, f)
			}
			continue
		}
		wz, cat := splitKey(f.Path)
		getOrNew(sum.ByWz, wz).add(f)
		getOrNew(sum.ByCat, wz+"/"+cat).add(f)
		sum.Total.add(f)
		for _, u := range f.Unres {
			if len(sum.UnresSample) < 30 {
				sum.UnresSample = append(sum.UnresSample, fmt.Sprintf("%s :: %s -> %s", f.Path, u.Path, u.Value))
			}
		}
		if n := atomic.AddInt64(&seen, 1); n%10000 == 0 {
			log.Printf("已解析 %d/%d，用时 %s", n, len(files), time.Since(start).Truncate(time.Second))
		}
	}
	elapsed := time.Since(start)
	sum.ElapsedMS = elapsed.Milliseconds()

	log.Printf("完成：成功 %d，失败 %d，节点 %d，uol %d（未解析 %d），耗时 %s",
		sum.Total.Files, sum.Total.Failed, sum.Total.Nodes, sum.Total.Uol, sum.Total.Unres,
		elapsed.Truncate(time.Millisecond))

	if *out != "" {
		data, err := json.MarshalIndent(&sum, "", "  ")
		if err != nil {
			log.Fatalf("序列化失败: %v", err)
		}
		if err := os.WriteFile(*out, data, 0o644); err != nil {
			log.Fatalf("写入 %s 失败: %v", *out, err)
		}
		log.Printf("统计已写入 %s", *out)
	}
	printTable(&sum)
}

func getOrNew(m map[string]*bucket, key string) *bucket {
	b, ok := m[key]
	if !ok {
		b = newBucket()
		m[key] = b
	}
	return b
}

func collectXML(abs string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(abs, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			log.Printf("遍历错误 %s: %v", p, err)
			return nil
		}
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".xml") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// run 用固定协程数解析，返回按完成顺序到达的结果通道（调用侧单线程消费，无需加锁）。
func run(abs string, files []string, workers int) <-chan fileStat {
	ch := make(chan string)
	out := make(chan fileStat, 512)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range ch {
				out <- parseOne(abs, p)
			}
		}()
	}
	go func() {
		defer close(ch)
		for _, p := range files {
			ch <- p
		}
	}()
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func parseOne(root, path string) fileStat {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	f := fileStat{Path: filepath.ToSlash(rel)}
	fi, err := os.Stat(path)
	if err != nil {
		f.Err = err.Error()
		return f
	}
	f.Size = fi.Size()
	f.ModTime = fi.ModTime().UTC().Format(time.RFC3339)

	n, info, err := wzxml.DecodeFileRobust(path)
	if err != nil {
		f.Err = err.Error()
		return f
	}
	f.Repaired, f.Encoding = info.RepairedTags, info.DeclaredEncoding
	st, err := wzxml.ResolveUol(n)
	if err != nil {
		f.Err = err.Error()
		return f
	}
	c := n.Count()
	f.Nodes, f.MaxDepth, f.Types = c.Nodes, c.MaxDepth, c.ByType
	f.UolFound, f.UolUnres, f.RootFixed = st.Found, st.Unresolved, st.RootFixed
	if st.Unresolved > 0 {
		f.Unres = n.ListUnresolved(5)
	}
	return f
}

// splitKey 把相对路径拆成 .wz 名与类别：Item.wz/Etc/0430.img.xml -> ("Item.wz","Etc")，
// Base.wz/smap.img.xml -> ("Base.wz","-")。
func splitKey(slashPath string) (wz, cat string) {
	parts := strings.Split(slashPath, "/")
	wz, cat = "?", "-"
	if len(parts) >= 1 {
		wz = parts[0]
	}
	if len(parts) >= 3 {
		cat = parts[1]
	}
	return wz, cat
}

func printTable(s *summary) {
	fmt.Printf("\n%-16s %7s %6s %12s %12s %10s %8s %9s\n",
		"WZ", "文件", "失败", "深度", "节点", "uol", "未解析", "字节")
	for _, k := range sortedKeys(s.ByWz) {
		d := s.ByWz[k]
		fmt.Printf("%-16s %7d %6d %12d %12d %10d %8d %9s\n",
			k, d.Files, d.Failed, d.MaxDepth, d.Nodes, d.Uol, d.Unres, human(d.Bytes))
	}
	t := &s.Total
	fmt.Printf("%-16s %7d %6d %12d %12d %10d %8d %9s\n",
		"合计", t.Files, t.Failed, t.MaxDepth, t.Nodes, t.Uol, t.Unres, human(t.Bytes))
	fmt.Printf("uol 根相对兜底成功 %d 个；修复截断标签 %d 处（涉及 %d 个文件）；编码声明归一 %d 个文件\n",
		t.RootFixed, t.RepairedTag, t.RepairedFile, t.EncFixed)

	fmt.Printf("\n%-26s %7s %12s %10s %8s\n", "WZ/类别", "文件", "节点", "uol", "未解析")
	for _, k := range sortedKeys(s.ByCat) {
		d := s.ByCat[k]
		fmt.Printf("%-26s %7d %12d %10d %8d\n", k, d.Files, d.Nodes, d.Uol, d.Unres)
	}

	fmt.Printf("\n节点类型合计：")
	types := make([]string, 0, len(t.Types))
	for k := range t.Types {
		types = append(types, k)
	}
	sort.Strings(types)
	for _, k := range types {
		fmt.Printf(" %s=%d", k, t.Types[k])
	}
	fmt.Println()

	if len(s.UnresSample) > 0 {
		fmt.Printf("\n未解析 uol 样例（最多 30 条，路径 :: 树内位置 -> 引用值）：\n")
		for _, u := range s.UnresSample {
			fmt.Printf("  %s\n", u)
		}
	}

	if len(s.Errors) > 0 {
		fmt.Printf("\n失败样例（最多 %d 条）：\n", len(s.Errors))
		for _, e := range s.Errors {
			fmt.Printf("  %s -> %s\n", e.Path, e.Err)
		}
	}
}

func sortedKeys(m map[string]*bucket) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.0fK", float64(n)/1024)
	}
}

var _ = atomic.AddInt64
