// Command wzbind 在真实导出目录上跑一遍"名称 + 属性按 ID 关联"，
// 用来验证核心目标（构建 ID+名称+属性 的完整物品元数据）到底覆盖了多少。
// 只读内存，不落库。
package main

import (
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

	"github.com/sqx6781268/MapleWzMeta/internal/binder"
	"github.com/sqx6781268/MapleWzMeta/internal/extract"
	"github.com/sqx6781268/MapleWzMeta/internal/wzxml"
)

type result struct {
	names []extract.NameEntry
	infos []extract.InfoEntry
	errs  []string
}

func main() {
	wz := flag.String("wz", filepath.Join(".", "wz"), "WZ 导出根目录")
	sources := flag.String("sources", "String.wz,Item.wz,Character.wz", "参与关联的 .wz 源，逗号分隔")
	workers := flag.Int("workers", runtime.NumCPU(), "并发解析协程数")
	charLimit := flag.Int("character-limit", 0, "Character.wz 最多解析多少个文件，0 表示全部")
	top := flag.Int("top", 15, "打印属性齐全物品的样例条数")
	flag.Parse()

	prefixes := strings.Split(*sources, ",")
	var tasks []string
	for _, s := range prefixes {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		dir := filepath.Join(*wz, s)
		fs, err := collect(dir)
		if err != nil {
			log.Fatalf("收集 %s 失败: %v", dir, err)
		}
		if s == "Character.wz" && *charLimit > 0 && len(fs) > *charLimit {
			fs = fs[:*charLimit]
		}
		log.Printf("%s: %d 个文件", s, len(fs))
		tasks = append(tasks, fs...)
	}

	var (
		mu      sync.Mutex
		res     result
		wg      sync.WaitGroup
		ch      = make(chan string, 512)
		start   = time.Now()
		counter int64
	)
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range ch {
				// rel 必须用 "/" 分隔：extract 按首段判源、次段判类别，
				// 直接拿 filepath.Rel 的 Windows 反斜杠结果会整批判错。
				rel, err := filepath.Rel(*wz, p)
				if err != nil {
					rel = p
				}
				rel = filepath.ToSlash(rel)
				n, _, err := wzxml.DecodeFileRobust(p)
				if err != nil {
					mu.Lock()
					if len(res.errs) < 50 {
						res.errs = append(res.errs, err.Error())
					}
					mu.Unlock()
					continue
				}
				if _, err := wzxml.ResolveUol(n); err != nil {
					mu.Lock()
					res.errs = append(res.errs, err.Error())
					mu.Unlock()
					continue
				}
				mu.Lock()
				if strings.HasPrefix(rel, "String.wz") {
					res.names = append(res.names, extract.Names(rel, n)...)
				} else {
					res.infos = append(res.infos, extract.Infos(rel, n)...)
				}
				mu.Unlock()
				if n := atomic.AddInt64(&counter, 1); n%5000 == 0 {
					log.Printf("已处理 %d/%d，用时 %s", n, len(tasks), time.Since(start).Truncate(time.Second))
				}
			}
		}()
	}
	go func() {
		for _, p := range tasks {
			ch <- p
		}
		close(ch)
	}()
	wg.Wait()

	items := binder.Bind(res.names, res.infos)
	st := binder.Summarize(items, 10)

	fmt.Printf("\n=== 关联结果（用时 %s）===\n", time.Since(start).Truncate(time.Second))
	fmt.Printf("名称条目 %d，属性条目 %d，去重后物品 ID %d\n", len(res.names), len(res.infos), len(items))
	fmt.Printf("名称+属性齐全 : %d (%.2f%%)\n", st.Both, pct(st.Both, len(items)))
	fmt.Printf("仅有名称      : %d (%.2f%%)\n", st.NameOnly, pct(st.NameOnly, len(items)))
	fmt.Printf("仅有属性(无名): %d (%.2f%%)\n", st.InfoOnly, pct(st.InfoOnly, len(items)))
	if len(st.NoNameIDs) > 0 {
		fmt.Printf("无名样例: %s\n", strings.Join(st.NoNameIDs, ", "))
	}
	if len(res.errs) > 0 {
		fmt.Printf("\n解析失败 %d 条，样例：\n", len(res.errs))
		for _, e := range res.errs[:min(len(res.errs), 10)] {
			fmt.Printf("  %s\n", e)
		}
	}

	if *top > 0 {
		fmt.Printf("\n=== 属性齐全物品样例 ===\n")
		shown := 0
		for _, it := range items {
			if !it.Complete() {
				continue
			}
			attrs := pickAttrs(it.Info)
			fmt.Printf("  %s  %-14s %-12s %s\n", it.ID, it.Name, it.Category, attrs)
			if shown++; shown >= *top {
				break
			}
		}
	}
}

func pickAttrs(m map[string]string) string {
	keys := []string{"reqLevel", "islot", "vslot", "price", "slotMax", "cash", "attack", "incPAD", "undead"}
	var parts []string
	for _, k := range keys {
		if v, ok := m[k]; ok {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, " ")
}

func collect(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".xml") {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) * 100 / float64(b)
}
