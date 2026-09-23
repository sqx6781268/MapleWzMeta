// Package icons 负责导出图片（imgdata）的索引与 HTTP 供给，两种来源同等对待：
//
//   - **目录**：`<dir>/<Wz 域>/<类别>/<id>.img.png`，Npc、Mob 等没有类别层，逐个文件走文件系统；
//   - **ZIP**：`<xxx>.zip` 指向打包后的同一棵树。启动时只解析中央目录建索引，
//     读取时按 `DataOffset` 用 `io.SectionReader` 直接定位原始字节（Store 模式下零解压、零拷贝），
//     几万个小文件不必落盘，读一张图的开销从 ~0.8ms 降到微秒级。
//
// 图片本身与语言无关，所以索引是全局的，只按 ID 查；ID 统一零填充到 8 位，
// 与各实体表的 id 对齐。
//
// 关键约束：**必须带域查询**。三个实体域共用同一个 8 位 ID 空间，
// `imgdata/Npc` 里 7,465 张图有 1,806 个 ID 与物品重合，
// 早先"按 Item > Character > 其他 取一张"的写法会让 1,578 个物品行
// 挂上 NPC 立绘（docs/附录 §6）。所以"其他"域不再参与物品取图，
// 每个实体只认自己那一个域。
package icons

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sqx6781268/MapleWzMeta/internal/extract"
)

// Suffix 是导出图片的文件名后缀。
const Suffix = ".img.png"

// maxImageSize 是单张图的解压上限。ZIP 中央目录里的 UncompressedSize64 由文件自己声明，
// 恶意构造的"解压炸弹"会在读取时按声明值分配内存；正常导出的图标都在几十 KB 量级，
// 超过这个尺寸一律不认（对应 CVE-2023-24538 一类的防护思路）。
const maxImageSize = 8 << 20

var idRe = regexp.MustCompile(`^[0-9]{1,12}$`)

// 实体域 → 允许的图片来源目录（imgdata 首层）。
var entityDomains = map[string][]string{
	"npc":     {"Npc"},
	"mob":     {"Mob"},
	"skill":   {"Skill"},
	"morph":   {"Morph"},
	"reactor": {"Reactor"},
	"":        {"Item", "Character"},
	"item":    {"Item", "Character"},
}

// knownDomains 用来识别"打包时多套了一层目录"的 ZIP（见 zipPrefix）。
var knownDomains = map[string]bool{
	"Item": true, "Character": true, "Npc": true, "Mob": true,
	"Skill": true, "Morph": true, "Reactor": true,
	"Eff": true, "Map": true,
}

// Index 是 (域, ID) → 图片位置的只读索引。目录模式与 ZIP 模式共用同一套查询与接口。
type Index struct {
	src      string // 来源显示名（目录或 zip 路径）
	isZip    bool
	file     *os.File    // zip 模式的底层句柄，实现 io.ReaderAt，进程期常驻
	zr       *zip.Reader // zip 模式的中央目录（只含元数据）
	byDomain map[string]map[string]entry
	byID     map[string]entry // 全域优先级最优，仅用于统计与未知域兜底
	files    int
	counts   map[string]int // 每个域索引到的文件数
}

type entry struct {
	path   string    // 目录模式：绝对路径；zip 模式：zip 内的条目名（仅用于显示与日志）
	fh     *zip.File // zip 模式的中央目录条目；目录模式为 nil
	rank   int
	size   int64
	modime int64
}

// Build 建立索引：dir 是目录就遍历，是 .zip 就解析中央目录。来源不可用时返回错误，由调用方降级。
func Build(dir string) (*Index, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("icons: 图标源 %s 不可用: %w", dir, err)
	}
	x := &Index{
		src:      dir,
		byDomain: map[string]map[string]entry{},
		byID:     map[string]entry{},
		counts:   map[string]int{},
	}
	if !st.IsDir() {
		return x, x.buildZip(dir)
	}
	return x, x.buildDir(dir)
}

// Close 释放 zip 模式的文件句柄；目录模式是空操作。
// Windows 下不关句柄会挡住临时目录的删除，测试与热替换都必须调它。
func (x *Index) Close() error {
	if x != nil && x.file != nil {
		err := x.file.Close()
		x.file = nil
		return err
	}
	return nil
}

// buildDir 遍历目录建立索引（原有方式，一张图一个文件）。
func (x *Index) buildDir(dir string) error {
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), Suffix) {
			return nil
		}
		id := extract.NormalizeID(strings.TrimSuffix(d.Name(), Suffix))
		if !idRe.MatchString(id) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		domain := domainOf(dir, p)
		x.add(id, domain, entry{path: p, rank: rankOf(domain), size: fi.Size(), modime: fi.ModTime().Unix()})
		return nil
	})
}

// buildZip 只解析中央目录：不解压任何数据，内存里留的是文件名 → FileHeader。
func (x *Index) buildZip(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return fmt.Errorf("icons: 打开归档 %s 失败: %w", p, err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		f.Close()
		return fmt.Errorf("icons: 解析归档 %s 失败: %w", p, err)
	}
	x.file, x.zr, x.isZip = f, zr, true

	prefix := zipPrefix(zr)
	for _, zf := range zr.File {
		name := strings.ReplaceAll(zf.Name, "\\", "/") // 老版 Compress-Archive 会写反斜杠
		if !strings.HasSuffix(name, Suffix) || strings.HasSuffix(name, "/") {
			continue
		}
		rel := name
		if prefix != "" {
			rel = strings.TrimPrefix(name, prefix+"/")
		}
		segs := strings.Split(rel, "/")
		domain, base := segs[0], segs[len(segs)-1]
		id := extract.NormalizeID(strings.TrimSuffix(base, Suffix))
		if !idRe.MatchString(id) {
			continue
		}
		if zf.UncompressedSize64 > maxImageSize {
			continue
		}
		x.add(id, domain, entry{
			path:   name,
			fh:     zf,
			rank:   rankOf(domain),
			size:   int64(zf.UncompressedSize64),
			modime: zf.Modified.Unix(),
		})
	}
	return nil
}

// zipPrefix 判断 ZIP 是否整体多套了一层目录：`Compress-Archive imgdata imgdata.zip`
// 得到的条目是 `imgdata/Character/...`，首段不是域名，不剥掉的话每张图都会被当成一个独立域。
// 判据是所有图片条目共享同一个首段，且该首段不是已知域（只打包单个域的 zip 不会被误剥）。
func zipPrefix(zr *zip.Reader) string {
	first := ""
	for _, zf := range zr.File {
		name := strings.ReplaceAll(zf.Name, "\\", "/")
		if !strings.HasSuffix(name, Suffix) || strings.HasSuffix(name, "/") {
			continue
		}
		segs := strings.Split(name, "/")
		if len(segs) < 3 {
			return "" // 已经有"域/文件"结构，再剥就过头
		}
		if first == "" {
			first = segs[0]
			continue
		}
		if first != segs[0] {
			return ""
		}
	}
	if first == "" || knownDomains[first] {
		return ""
	}
	return first
}

// add 把一张图登记进两个索引；同域同 ID 多张时按 rank 取优。
func (x *Index) add(id, domain string, e entry) {
	x.files++
	x.counts[domain]++
	bucket := x.byDomain[domain]
	if bucket == nil {
		bucket = map[string]entry{}
		x.byDomain[domain] = bucket
	}
	if old, ok := bucket[id]; !ok || old.rank > e.rank {
		bucket[id] = e
	}
	if old, ok := x.byID[id]; !ok || old.rank > e.rank {
		x.byID[id] = e
	}
}

// read 取出一张图的字节。zip + Store 走 SectionReader 直接按偏移读，
// 绕开 flate 包装器；其余（deflate 打包的归档、或拿不到数据偏移）才回退到 fh.Open()。
func (x *Index) read(e entry) ([]byte, error) {
	if e.fh == nil {
		return os.ReadFile(e.path)
	}
	if e.fh.Method == zip.Store && e.fh.CompressedSize64 == e.fh.UncompressedSize64 {
		if off, err := e.fh.DataOffset(); err == nil {
			n := int64(e.fh.UncompressedSize64)
			buf := make([]byte, n)
			sr := io.NewSectionReader(x.file, off, n)
			if _, err := io.ReadFull(sr, buf); err != nil {
				return nil, fmt.Errorf("icons: 读取 %s 失败: %w", e.path, err)
			}
			return buf, nil
		}
	}
	rc, err := e.fh.Open()
	if err != nil {
		return nil, fmt.Errorf("icons: 打开 %s 失败: %w", e.path, err)
	}
	defer rc.Close()
	buf, err := io.ReadAll(io.LimitReader(rc, maxImageSize))
	if err != nil {
		return nil, fmt.Errorf("icons: 解压 %s 失败: %w", e.path, err)
	}
	return buf, nil
}

// domainOf 取图片所属的 imgdata 首层目录名（即来源 WZ 包）。
func domainOf(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return ""
	}
	return strings.Split(filepath.ToSlash(rel), "/")[0]
}

// rankOf 给同一域内的同号多张图定优先级（类别目录不同的情况）；跨域不比较。
func rankOf(domain string) int {
	switch domain {
	case "Item":
		return 0
	case "Character":
		return 1
	case "Npc":
		return 2
	case "Mob":
		return 3
	case "Skill":
		return 4
	case "Morph":
		return 5
	case "Reactor":
		return 6
	default:
		return 9
	}
}

// Count 返回索引到的图片文件总数（含同 ID 多张）。
func (x *Index) Count() int { return x.files }

// IDs 返回去重后的可用 ID 数（跨域合并口径，与旧版统计一致）。
func (x *Index) IDs() int { return len(x.byID) }

// Path 按 (实体域, ID) 取图片绝对路径；ID 先做零填充归一。
func (x *Index) Path(entity, id string) (string, bool) {
	e, ok := x.entry(entity, id)
	if !ok {
		return "", false
	}
	return e.path, true
}

func (x *Index) entry(entity, id string) (entry, bool) {
	key := extract.NormalizeID(id)
	domains, known := entityDomains[entity]
	if !known {
		// 未知实体域退回"全域最优"，保证老调用方不会因为传了陌生值就拿不到图。
		e, ok := x.byID[key]
		return e, ok
	}
	var best entry
	found := false
	for _, dm := range domains {
		e, ok := x.byDomain[dm][key]
		if !ok {
			continue
		}
		if !found || e.rank < best.rank {
			best, found = e, true
		}
	}
	return best, found
}

// Has 判断某实体域的某 ID 是否有图标，供列表接口回填 icon 字段。
func (x *Index) Has(entity, id string) bool {
	_, ok := x.entry(entity, id)
	return ok
}

// Serve 处理 /img/<id>.png?for=<实体域>。只接受纯数字 ID，路径一律取自索引，不存在目录穿越。
// 目录模式交给 http.ServeFile（保留 Range/Last-Modified）；zip 模式直接写字节，不经文件系统。
func (x *Index) Serve(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/img/")
	id := strings.TrimSuffix(name, ".png")
	if !idRe.MatchString(id) {
		http.Error(w, "图标名不合法", http.StatusBadRequest)
		return
	}
	e, ok := x.entry(r.URL.Query().Get("for"), id)
	if !ok {
		http.Error(w, "无此图标", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if !x.isZip {
		http.ServeFile(w, r, e.path)
		return
	}
	data, err := x.read(e)
	if err != nil {
		http.Error(w, "图标读取失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Write(data)
}

// WarmLog 打印索引概况，便于确认图标是否真的接上了。
func (x *Index) WarmLog() {
	domains := make([]string, 0, len(x.counts))
	for d := range x.counts {
		domains = append(domains, d)
	}
	sort.Strings(domains)
	parts := make([]string, 0, len(domains))
	for _, d := range domains {
		parts = append(parts, fmt.Sprintf("%s=%d", d, x.counts[d]))
	}
	var perEntity []string
	for _, e := range []string{"item", "npc", "mob", "skill", "morph", "reactor"} {
		perEntity = append(perEntity, fmt.Sprintf("%s=%d", e, len(x.idsOf(e))))
	}
	src := "目录"
	if x.isZip {
		src = "归档"
	}
	log.Printf("图标索引（%s %s）：%d 个文件，%d 个可用 ID（%s）；按域：%s",
		src, x.src, x.files, len(x.byID), strings.Join(parts, " "), strings.Join(perEntity, "，"))
}

// idsOf 列出某域涉及的 ID 集合，仅 WarmLog 统计用。
func (x *Index) idsOf(entity string) []string {
	domains, ok := entityDomains[entity]
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, dm := range domains {
		for id := range x.byDomain[dm] {
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// CopyTo 把某 (实体域, ID) 的图片写进 w，供导出或测试使用。
func (x *Index) CopyTo(w io.Writer, entity, id string) (bool, error) {
	e, ok := x.entry(entity, id)
	if !ok {
		return false, nil
	}
	data, err := x.read(e)
	if err != nil {
		return true, err
	}
	_, err = w.Write(data)
	return true, err
}
