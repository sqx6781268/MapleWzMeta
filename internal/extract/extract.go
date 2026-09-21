// Package extract 把解码后的 WZ 节点树抽取成可按物品 ID 关联的记录。
//
// 实测数据来源有三处，层级并不一致：
//   - String.wz/{Cash,Consume,Ins,Pet}.img.xml：根 imgdir 直接挂 7 位 ID；
//   - String.wz/Etc.img.xml：根下多一层 "Etc" 再挂 7 位 ID；
//   - String.wz/Eqp.img.xml：根下是 "Eqp" 再按部位（Accessory 等）分层，然后 7 位 ID；
//   - Item.wz/<类别>/<组>.img.xml：根下是 8 位 ID，属性在其 info 子节点；
//   - Character.wz/<部位>/<id>.img.xml：根 imgdir 名即 8 位 ID，info 在根下。
//
// 因此识别 ID 不依赖固定层数，而是"名字为 7~8 位纯数字的 imgdir 即物品节点"，
// 并统一零填充到 8 位，才能跨源对齐。
package extract

import (
	"path"
	"strconv"
	"strings"

	"github.com/sqx6781268/MapleWzMeta/internal/wzxml"
)

// IDLen 是物品 ID 归档用的定长位数。
const IDLen = 8

// NormalizeID 去掉 ".img" 后缀并把纯数字 ID 零填充到 8 位。
// 非纯数字（如 Mob 的 6 位、Npc 的对话分组）原样返回，交由调用方区分域。
func NormalizeID(raw string) string {
	id := strings.TrimSuffix(strings.TrimSpace(raw), ".img")
	if !isDigits(id) {
		return id
	}
	if len(id) >= IDLen {
		return id
	}
	return strings.Repeat("0", IDLen-len(id)) + id
}

// LooksLikeItemID 判断是否为可跨源关联的物品 ID（7 或 8 位纯数字）。
func LooksLikeItemID(raw string) bool {
	id := strings.TrimSuffix(strings.TrimSpace(raw), ".img")
	return isDigits(id) && (len(id) == 7 || len(id) == IDLen)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// NameEntry 是一条名称/描述记录，来自 String.wz。
type NameEntry struct {
	ID       string
	Name     string
	Desc     string
	Category string
	Extra    map[string]string
}

// Names 收集 String.wz 中所有带 name 子节点的 ID 节点。
// 遍历整棵树，命中条件：imgdir 且 name 形如物品 ID 且存在 string 名 "name"。
func Names(root *wzxml.Node) []NameEntry {
	var out []NameEntry
	root.Walk(func(parent, n *wzxml.Node) bool {
		if n.Type != "imgdir" || !LooksLikeItemID(n.Name) {
			return true
		}
		name := n.Child("name")
		if name == nil || name.Type != "string" {
			return true
		}
		e := NameEntry{
			ID:       NormalizeID(n.Name),
			Name:     name.Value,
			Category: categoryOf(parent),
		}
		if d := n.Child("desc"); d != nil && d.Type == "string" {
			e.Desc = d.Value
		}
		for _, c := range n.Children {
			if c.Type != "string" || c.Name == "name" || c.Name == "desc" {
				continue
			}
			if e.Extra == nil {
				e.Extra = map[string]string{}
			}
			e.Extra[c.Name] = c.Value
		}
		out = append(out, e)
		return true
	})
	return out
}

// InfoEntry 是一个物品的属性集合，来自 Item.wz 或 Character.wz。
type InfoEntry struct {
	ID         string
	Category   string
	Source     string
	Info       map[string]string
	Parts      []string // info 之外的子节点名（外观动画组等），用于判断数据完整度
	HasInfoDir bool
}

// Infos 收集一个 img 文件里的物品属性。
// rel 形如 "Item.wz/Etc/0430.img.xml" 或 "Character.wz/Cap/01002936.img.xml"。
func Infos(rel string, root *wzxml.Node) []InfoEntry {
	source := sourceOf(rel)
	category := categoryOfDir(rel)

	// Character.wz 一个文件就是一个物品：根节点名即 ID。
	if source == "Character.wz" && LooksLikeItemID(root.Name) {
		return []InfoEntry{entryFromInfo(root, NormalizeID(root.Name), category, source)}
	}

	var out []InfoEntry
	for _, c := range root.Children {
		if c.Type != "imgdir" || !LooksLikeItemID(c.Name) {
			continue
		}
		out = append(out, entryFromInfo(c, NormalizeID(c.Name), category, source))
	}
	return out
}

func entryFromInfo(node *wzxml.Node, id, category, source string) InfoEntry {
	e := InfoEntry{ID: id, Category: category, Source: source, Parts: node.ChildNames()}
	info := node.Child("info")
	if info == nil {
		return e
	}
	e.HasInfoDir = true
	e.Info = flattenScalars(info)
	return e
}

// flattenScalars 把 imgdir 下的标量属性摊平一层，嵌套结构只记名字。
func flattenScalars(n *wzxml.Node) map[string]string {
	out := map[string]string{}
	for _, c := range n.Children {
		switch c.Type {
		case "imgdir":
			continue
		case "canvas", "vector":
			// 画布/向量按 "父.子.属性" 记，例如 icon.origin.x
			if len(c.Children) == 0 {
				for k, v := range c.Attrs {
					out[c.Name+"."+k] = v
				}
				continue
			}
			for k, v := range c.Attrs {
				out[c.Name+"."+k] = v
			}
			for _, sub := range c.Children {
				for k, v := range sub.Attrs {
					out[c.Name+"."+sub.Name+"."+k] = v
				}
			}
		default:
			out[c.Name] = c.Value
		}
	}
	for k, v := range n.Attrs {
		out["@"+k] = v
	}
	return out
}

// categoryOf 取 String.wz 里的分组名：直接挂 ID 时（父节点就是文件根）返回空，
// 分层时返回分层目录名，如 Eqp/Accessory、Etc。
func categoryOf(parent *wzxml.Node) string {
	if parent == nil || strings.HasSuffix(parent.Name, ".img") {
		return ""
	}
	if LooksLikeItemID(parent.Name) {
		return ""
	}
	return parent.Name
}

// sourceOf 取 "Item.wz/Etc/0430.img.xml" 的首段。
func sourceOf(rel string) string {
	if i := strings.IndexByte(rel, '/'); i > 0 {
		return rel[:i]
	}
	return rel
}

// categoryOfDir 取次级目录名。文件直接位于 .wz 根下（如 Character.wz/00002000.img.xml）
// 时返回空串，交给名称侧或上层显示为"未分类"，不要写 "-" 这种哨兵值进库。
func categoryOfDir(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) >= 3 {
		return parts[len(parts)-2]
	}
	return ""
}

// RootID 从相对路径推断单文件物品 ID（Character.wz 用）。
func RootID(rel string) string {
	base := path.Base(rel)
	base = strings.TrimSuffix(base, ".xml")
	if !LooksLikeItemID(base) {
		return ""
	}
	return NormalizeID(base)
}

// IsNumeric 供上层做数据校验。
func IsNumeric(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}
