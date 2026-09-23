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

	// Paths 非空时只处理这些相对路径（形如 "Character.wz/Cap/01002936.img.xml"），
	// 用于管理页"指定文件重载"。集合外的文件一律不动。
	Paths []string
	// Force 为 true 时跳过 size+mtime 增量判定，强制重解析。
	Force bool
	// Overwrite 为 true 时按"以文件为准"落库：名称直接覆盖、info 整包替换，
	// 并清掉该文件不再贡献的历史 ID（按 kind:id 归属判断）。关掉则维持只增不减的合并语义。
	Overwrite bool
	// SkipEdited 为 true 时跳过管理页手工改过的行（按实体域各自判断，Overwrite 下才有效果）。
	SkipEdited bool
	// OnProgress 每处理完一批回调一次（done 已处理数，total 本次待处理总数），供管理页显示进度。
	OnProgress func(done, total int)
}

// Result 是一次扫描的产出统计。
type Result struct {
	Found     int // 磁盘上的候选文件数
	Skipped   int // 指纹未变而跳过的文件数
	Parsed    int // 实际解析成功的文件数
	Failed    int // 解析失败的文件数
	Names     int // 写入的名称条目数
	Infos     int // 写入的属性条目数
	Cleared   int // 清掉的失效贡献数（Overwrite 下 XML 里已删除的 ID）
	Protected int // 因手工修改标记而跳过的条目数
	UolFixed  int // uol 展开数
	Repaired  int // 容错修复的标签数
	Duration  time.Duration
	Errors    []string // 失败样例，最多留 20 条
	// Missing 是指定 Paths 里磁盘上找不到的路径（管理页据此提示"该文件已不存在"）。
	Missing []string
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
	if res.Found == 0 && len(opts.Paths) == 0 {
		return res, fmt.Errorf("scan: %s 下没有找到任何 .xml", opts.Root)
	}

	recs := make([]store.FileRec, len(entries))
	for i, e := range entries {
		recs[i] = store.FileRec{Path: e.rel, Size: e.size, ModTime: e.modTime}
	}
	pending := recs
	if !opts.Force {
		pending, err = db.Pending(opts.Lang, recs)
		if err != nil {
			return res, fmt.Errorf("scan: 增量判定失败: %w", err)
		}
	}
	res.Skipped = res.Found - len(pending)
	// 指定文件重载：Paths 是显式清单，优先于增量判定（管理页要求"点了就必须重解析"）。
	// 清单里以 "/" 结尾的条目按目录前缀展开。
	if len(opts.Paths) > 0 {
		pending, res.Missing = pickByPath(recs, opts.Paths)
		res.Skipped = res.Found - len(pending)
	}
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

	// 覆盖语义要先知道"这个文件历史上贡献了哪些 ID"，才能清掉如今解析不到的那些。
	var prevIDs map[string][]store.IDRef
	if opts.Overwrite {
		paths := make([]string, 0, len(todo))
		for _, e := range todo {
			paths = append(paths, e.rel)
		}
		prevIDs, err = db.FileIDsByPaths(opts.Lang, paths)
		if err != nil {
			return res, fmt.Errorf("scan: 读取历史溯源失败: %w", err)
		}
	}

	runID, err := db.StartRun(opts.Lang, len(todo), runNote(opts))
	if err != nil {
		return res, err
	}

	live := make([]store.FileRec, 0, len(pending))
	var nameBuf []store.NameArg
	var infoBuf []store.InfoArg
	var uolFixed, repaired, parsed, failed int

	flush := func() error {
		if opts.Overwrite {
			for _, r := range live {
				stale := store.StaleIDs(prevIDs[r.Path], r.Kind, r.IDs)
				if len(stale) == 0 {
					continue
				}
				n, err := db.ClearSide(opts.Lang, stale, store.SideOfPath(r.Path), r.Path, opts.SkipEdited)
				if err != nil {
					return err
				}
				res.Cleared += n
			}
		}
		if err := db.SaveFiles(opts.Lang, live); err != nil {
			return err
		}
		// 落库顺序与并发到达顺序解耦：同一个 ID 被多个文件写入时，胜者由 (ID, 来源路径)
		// 的定序决定，不再取决于哪个协程先跑完（文件数或机器负载一变就可能翻转）。
		sort.Slice(nameBuf, func(i, j int) bool {
			if nameBuf[i].ID != nameBuf[j].ID {
				return nameBuf[i].ID < nameBuf[j].ID
			}
			return nameBuf[i].Src < nameBuf[j].Src
		})
		sort.Slice(infoBuf, func(i, j int) bool {
			if infoBuf[i].ID != infoBuf[j].ID {
				return infoBuf[i].ID < infoBuf[j].ID
			}
			return infoBuf[i].Src < infoBuf[j].Src
		})
		if opts.Overwrite {
			n, err := db.PutNamesForce(opts.Lang, nameBuf, opts.SkipEdited)
			if err != nil {
				return err
			}
			m, err := db.PutInfosForce(opts.Lang, infoBuf, opts.SkipEdited)
			if err != nil {
				return err
			}
			res.Protected += (len(nameBuf) - n) + (len(infoBuf) - m)
		} else {
			if _, err := db.PutNames(opts.Lang, nameBuf); err != nil {
				return err
			}
			if _, err := db.PutInfos(opts.Lang, infoBuf); err != nil {
				return err
			}
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
			if opts.OnProgress != nil && n%100 == 0 {
				opts.OnProgress(n, len(todo))
			}
		}
		if opts.OnProgress != nil {
			opts.OnProgress(n, len(todo))
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
	// 定向重载不做全局清理：那会让"只重载 3 个文件"顺手删掉无关文件的贡献。
	if len(opts.Paths) == 0 {
		if n, err := db.DeleteMissing(opts.Lang, recPaths(recs)); err != nil {
			log.Printf("清理失效文件记录失败: %v", err)
		} else if n > 0 && !opts.Quiet {
			log.Printf("已清理 %d 条磁盘上不再存在的文件记录", n)
		}
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
		// 名称侧只认白名单里的文本表。实测 `String.wz/Mob`、`/Npc`、`/Skill` 的键
		// 补零后与物品 ID 100% 重叠，整目录按名称入库就是 4,347 条跨实体污染。
		kind, ok := extract.NameKindOf(e.rel)
		fo.rec.Kind = store.Kind(kind)
		if !ok {
			return fo
		}
		for _, ne := range extract.Names(e.rel, root) {
			fo.names = append(fo.names, store.NameArg{
				Kind: store.Kind(kind), ID: ne.ID, Name: ne.Name, Descr: ne.Desc,
				Category: ne.Category, Src: e.rel,
			})
		}
	} else {
		kind := store.Kind(extract.KindOf(e.rel))
		fo.rec.Kind = kind
		for _, ie := range extract.Infos(e.rel, root) {
			if len(ie.Info) == 0 {
				continue
			}
			fo.infos = append(fo.infos, store.InfoArg{
				Kind: kind, ID: ie.ID, Category: ie.Category, Info: ie.Info, Src: e.rel,
			})
		}
	}
	// 溯源清单：管理页重解析后要据此判断"哪些 ID 是这个文件贡献的"。
	fo.rec.IDs = contributedIDs(fo.names, fo.infos)
	fo.rec.Rows = len(fo.names) + len(fo.infos)
	fo.rec.Repaired = fo.repaired
	return fo
}

// contributedIDs 汇总本文件贡献的实体 ID（不含域前缀，域记在 FileRec.Kind），去重升序。
func contributedIDs(names []store.NameArg, infos []store.InfoArg) []string {
	set := make(map[string]bool, len(names)+len(infos))
	out := make([]string, 0, len(names)+len(infos))
	for _, a := range names {
		if !set[a.ID] {
			set[a.ID] = true
			out = append(out, a.ID)
		}
	}
	for _, a := range infos {
		if !set[a.ID] {
			set[a.ID] = true
			out = append(out, a.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Collect 返回磁盘上的候选文件指纹（相对路径正斜杠、按路径升序），供管理页做比对。
func Collect(root string, sources []string) ([]store.FileRec, error) {
	entries, err := collect(root, sources)
	if err != nil {
		return nil, err
	}
	out := make([]store.FileRec, len(entries))
	for i, e := range entries {
		out[i] = store.FileRec{Path: e.rel, Size: e.size, ModTime: e.modTime}
	}
	return out, nil
}

// pickByPath 按显式清单挑出磁盘上存在的文件；清单里匹配不到任何文件的选择符回在 missing 里。
// 选择符以 "/" 结尾时按目录前缀展开，否则要求路径完全相等（都可省略 Root 前缀）。
func pickByPath(recs []store.FileRec, paths []string) (matched []store.FileRec, missing []string) {
	exact := map[string]bool{}
	var prefixes []string
	for _, p := range paths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, "/") {
			prefixes = append(prefixes, p)
			continue
		}
		exact[p] = true
	}
	usedPrefix := map[string]bool{}
	hit := map[string]bool{}
	for _, r := range recs {
		if exact[r.Path] {
			matched = append(matched, r)
			hit[r.Path] = true
			continue
		}
		for _, pre := range prefixes {
			if strings.HasPrefix(r.Path, pre) {
				matched = append(matched, r)
				usedPrefix[pre] = true
				break
			}
		}
	}
	for p := range exact {
		if !hit[p] {
			missing = append(missing, p)
		}
	}
	for _, pre := range prefixes {
		if !usedPrefix[pre] {
			missing = append(missing, pre)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Path < matched[j].Path })
	sort.Strings(missing)
	return matched, missing
}

// runNote 是扫描历史的备注，区分常规增量扫描与各种强制重载。
func runNote(opts Options) string {
	src := strings.Join(opts.Sources, ",")
	if len(opts.Paths) > 0 {
		return fmt.Sprintf("reload:%d %s", len(opts.Paths), src)
	}
	if opts.Force {
		return "force " + src
	}
	return src
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
			// 未导出的源目录跳过而非报错：语言包常只导出部分 wz，缺目录不应阻断其余源的扫描。
			continue
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
