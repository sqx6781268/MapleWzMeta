// Package icons 负责导出图片目录（imgdata）的索引与 HTTP 供给。
//
// 实测布局：<dir>/<Wz>/<类别>/<id>.img.png，Npc 等没有类别层。
// 图片本身与语言无关，所以索引是全局的，只按物品 ID 查；ID 统一零填充到 8 位，
// 与 item.id 对齐。不同 Wz 包出现同号文件时按 Item > Character > 其他 取优先级。
package icons

import (
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

var idRe = regexp.MustCompile(`^[0-9]{1,12}$`)

// Index 是 ID → 图片路径的只读索引。
type Index struct {
	dir   string
	byID  map[string]entry
	files int
}

type entry struct {
	path   string
	rank   int
	size   int64
	modime int64
}

// Build 遍历 dir 建立索引。dir 不存在时返回错误，由调用方决定是否降级。
func Build(dir string) (*Index, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("icons: 目录 %s 不可用: %w", dir, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("icons: %s 不是目录", dir)
	}
	x := &Index{dir: dir, byID: map[string]entry{}}
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), Suffix) {
			return nil
		}
		id := extract.NormalizeID(strings.TrimSuffix(d.Name(), Suffix))
		if !extract.LooksLikeItemID(id) && !idRe.MatchString(id) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		x.files++
		e := entry{path: p, rank: rankOf(dir, p), size: fi.Size(), modime: fi.ModTime().Unix()}
		if old, ok := x.byID[id]; ok && old.rank <= e.rank {
			return nil
		}
		x.byID[id] = e
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("icons: 建立索引失败: %w", err)
	}
	return x, nil
}

// rankOf 按图片所在的首层目录定优先级：物品/装备优先于 Npc、Mob 等其他实体域。
func rankOf(root, p string) int {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return 9
	}
	head := strings.Split(filepath.ToSlash(rel), "/")[0]
	switch head {
	case "Item":
		return 0
	case "Character":
		return 1
	default:
		return 5
	}
}

// Count 返回索引到的图片数量（含被覆盖的同 ID 文件）。
func (x *Index) Count() int { return x.files }

// IDs 返回索引条目数（去重后的 ID 数）。
func (x *Index) IDs() int { return len(x.byID) }

// Path 按 ID 取图片绝对路径；ID 会先做零填充归一。
func (x *Index) Path(id string) (string, bool) {
	e, ok := x.byID[extract.NormalizeID(id)]
	if !ok {
		return "", false
	}
	return e.path, true
}

// Has 判断某 ID 是否有图标，供列表接口回填 icon 字段。
func (x *Index) Has(id string) bool {
	_, ok := x.Path(id)
	return ok
}

// Serve 处理 /img/<id>.png。只接受纯数字 ID，路径来自索引，不存在目录穿越。
func (x *Index) Serve(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/img/")
	id := strings.TrimSuffix(name, ".png")
	if !idRe.MatchString(id) {
		http.Error(w, "图标名不合法", http.StatusBadRequest)
		return
	}
	path, ok := x.Path(id)
	if !ok {
		http.Error(w, "无此图标", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, path)
}

// WarmLog 打印索引概况，便于确认图标是否真的接上了。
func (x *Index) WarmLog() {
	byRank := map[int]int{}
	for _, e := range x.byID {
		byRank[e.rank]++
	}
	keys := make([]int, 0, len(byRank))
	for k := range byRank {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("优先级%d=%d", k, byRank[k]))
	}
	log.Printf("图标索引：%d 个文件，%d 个可用 ID（%s）", x.files, len(x.byID), strings.Join(parts, " "))
}

// CopyTo 把某 ID 的图片写进 w，供导出或测试使用。
func (x *Index) CopyTo(w io.Writer, id string) (bool, error) {
	path, ok := x.Path(id)
	if !ok {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return true, err
}
