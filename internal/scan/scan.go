// Package scan 把"WZ 导出目录 → 解析 → 按 ID 关联 → SQLite 落库"串成一条可重跑的管线。
//
// 增量依据是文件的 size + mtime：导出器保留了源文件时间戳，robocopy 复制也不破坏，
// 因此未变文件可以直接跳过解析（全量约 4.5 万个 Character.wz 文件，重跑代价很高）。
package scan

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sqx6781268/MapleWzMeta/internal/extract"
	"github.com/sqx6781268/MapleWzMeta/internal/store"
	"github.com/sqx6781268/MapleWzMeta/internal/wzxml"
)

// Options 是一次扫描的输入。
type Options struct {
	Lang    string   // 语言域，如 zh-CN；同一库内按语言分域存放
	Root    string   // WZ 导出根目录
	Sources []string // 参与关联的 .wz 名，空表示根目录下全部
	Workers int      // 并发解析协程数，<=0 取 CPU 数
	Limit   int      // 最多处理多少个文件，0 表示不限（调试用）
	Batch   int      // 每攒多少文件写一次库，<=0 取 400
	Quiet   bool     // 关掉进度日志
}

// Result 是一次扫描的产出统计。
type Result struct {
	Found    int // 磁盘上的候选文件数
	Skipped  int // 指纹未变而跳过的文件数
	Parsed   int // 实际解析成功的文件数
	Failed   int // 解析失败的文件数
	Names    int // 写入的名称条目数
	Infos    int // 写入的属性条目数
	UolFixed int // uol 展开数
	Repaired int // 容错修复的标签数
	Duration time.Duration
	Errors   []string // 失败样例，最多留 20 条
}

// entry 是磁盘上的一个待处理文件。
type entry struct {
	path    string
	rel     string // 相对 Root，始终用 "/" 分隔
	size    int64
	modTime time.Time
}

// Run 执行扫描并把结果写入 db。db 中已有的记录不会被清空，只按指纹增量更新。
func Run(opts Options, db *store.DB) (Result, error) {
	res := Result{}
	if opts.Root == "" {
		return res, fmt.Errorf("scan: Root 不能为空")
	}
	if opts.Lang == "" {
		return res, fmt.Errorf("scan: Lang 不能为空")
	}
	if _, err := os.Stat(opts.Root); err != nil {
		return res, fmt.Errorf("scan: 目录不可用: %w", err)
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	batch := opts.Batch
	if batch <= 0 {
		batch = 400
	}

	entries, err := collect(opts.Root, opts.Sources)
	if err != nil {
		return res, err
	}
	res.Found = len(entries)
	if res.Found == 0 {
		return res, fmt.Errorf("scan: %s 下没有找到任何 .xml", opts.Root)
	}

	recs := make([]store.FileRec, len(entries))
	for i, e := range entries {
		recs[i] = store.FileRec{Path: e.rel, Size: e.size, ModTime: e.modTime}
	}
	pending, err := db.Pending(opts.Lang, recs)
	if err != nil {
		return res, fmt.Errorf("scan: 增量判定失败: %w", err)
	}
	res.Skipped = res.Found - len(pending)
	if opts.Limit > 0 && len(pending) > opts.Limit {
		pending = pending[:opts.Limit]
	}
	// Pending 只回指纹，这里换回带绝对路径的 entry。
	todo := make([]entry, 0, len(pending))
	want := make(map[string]bool, len(pending))
	for _, r := range pending {
		want[r.Path] = true
	}
	for _, e := range entries {
		if want[e.rel] {
			todo = append(todo, e)
		}
	}

	runID, err := db.StartRun(opts.Lang, len(todo), strings.Join(opts.Sources, ","))
	if err != nil {
		return res, err
	}

	live := make([]store.FileRec, 0, len(pending))
	var nameBuf []store.NameArg
	var infoBuf []store.InfoArg
	var uolFixed, repaired, parsed, failed int

	flush := func() error {
		if err := db.SaveFiles(opts.Lang, live); err != nil {
			return err
		}
		if _, err := db.PutNames(opts.Lang, nameBuf); err != nil {
			return err
		}
		if _, err := db.PutInfos(opts.Lang, infoBuf); err != nil {
			return err
		}
		res.Names += len(nameBuf)
		res.Infos += len(infoBuf)
		live, nameBuf, infoBuf = live[:0], nameBuf[:0], infoBuf[:0]
		return nil
	}

	start := time.Now()
	ch := make(chan entry, 512)
	out := make(chan fileOut, 512)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range ch {
				out <- process(e)
			}
		}()
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	go func() {
		for _, e := range todo {
			ch <- e
		}
		close(ch)
	}()

	done := make(chan error, 1)
	go func() {
		var n int
		for fo := range out {
			n++
			if fo.err != "" {
				failed++
				if len(res.Errors) < 20 {
					res.Errors = append(res.Errors, fo.err)
				}
			} else {
				parsed++
				uolFixed += fo.uol
				repaired += fo.repaired
			}
			live = append(live, fo.rec)
			nameBuf = append(nameBuf, fo.names...)
			infoBuf = append(infoBuf, fo.infos...)
			if len(live) >= batch {
				if err := flush(); err != nil {
					done <- err
					return
				}
			}
			if !opts.Quiet && n%2000 == 0 {
				log.Printf("已处理 %d/%d（跳过 %d），用时 %s", n, len(todo), res.Skipped, time.Since(start).Truncate(time.Second))
			}
		}
		done <- flush()
	}()
	if err := <-done; err != nil {
		db.FinishRun(runID, parsed, failed)
		return res, fmt.Errorf("scan: 落库失败: %w", err)
	}

	res.Parsed, res.Failed = parsed, failed
	res.UolFixed, res.Repaired = uolFixed, repaired
	res.Duration = time.Since(start)
	if err := db.FinishRun(runID, parsed, failed); err != nil {
		log.Printf("扫描收尾记录失败: %v", err)
	}
	if n, err := db.DeleteMissing(opts.Lang, recPaths(recs)); err != nil {
		log.Printf("清理失效文件记录失败: %v", err)
	} else if n > 0 && !opts.Quiet {
		log.Printf("已清理 %d 条磁盘上不再存在的文件记录", n)
	}
	return res, nil
}

// fileOut 是单个文件的处理产出。
type fileOut struct {
	rec      store.FileRec
	names    []store.NameArg
	infos    []store.InfoArg
	uol      int
	repaired int
	err      string
}

func process(e entry) fileOut {
	fo := fileOut{rec: store.FileRec{
		Path: e.rel, Size: e.size, ModTime: e.modTime, Status: "ok",
	}}
	root, info, err := wzxml.DecodeFileRobust(e.path)
	if err != nil {
		fo.rec.Status = "failed"
		fo.rec.Err = err.Error()
		fo.err = fmt.Sprintf("%s: %v", e.rel, err)
		return fo
	}
	fo.repaired = info.RepairedTags
	uol, err := wzxml.ResolveUol(root)
	if err != nil {
		fo.rec.Status = "failed"
		fo.rec.Err = err.Error()
		fo.err = fmt.Sprintf("%s: uol 展开失败: %v", e.rel, err)
		return fo
	}
	fo.uol = uol.Resolved

	if strings.HasPrefix(e.rel, "String.wz") {
		for _, ne := range extract.Names(root) {
			fo.names = append(fo.names, store.NameArg{ID: ne.ID, Name: ne.Name, Descr: ne.Desc, Category: ne.Category})
		}
	} else {
		for _, ie := range extract.Infos(e.rel, root) {
			if len(ie.Info) == 0 {
				continue
			}
			fo.infos = append(fo.infos, store.InfoArg{ID: ie.ID, Category: ie.Category, Info: ie.Info})
		}
	}
	fo.rec.Rows = len(fo.names) + len(fo.infos)
	fo.rec.Repaired = fo.repaired
	return fo
}

// collect 按源收集 .xml 文件；Sources 为空时扫描整个根目录。
func collect(root string, sources []string) ([]entry, error) {
	var dirs []string
	if len(sources) == 0 {
		dirs = []string{root}
	} else {
		for _, s := range sources {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			dirs = append(dirs, filepath.Join(root, s))
		}
	}
	var out []entry
	for _, dir := range dirs {
		st, err := os.Stat(dir)
		if err != nil {
			return nil, fmt.Errorf("scan: 源目录 %s 不可用: %w", dir, err)
		}
		if !st.IsDir() {
			continue
		}
		err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".xml") {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				rel = p
			}
			out = append(out, entry{
				path:    p,
				rel:     filepath.ToSlash(rel),
				size:    fi.Size(),
				modTime: fi.ModTime(),
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

func recPaths(recs []store.FileRec) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Path
	}
	return out
}
