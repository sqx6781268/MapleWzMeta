package wzxml

import (
	"fmt"
	"sort"
	"strings"
)

// MaxUolPass 限制 uol 打平的重趟数，防止自引用链无限展开。
const MaxUolPass = 16

// UolStats 记录一次 uol 打平的结果。
type UolStats struct {
	Found      int // 树中出现的 uol 节点数
	Resolved   int // 成功展开为目标子树的数
	RootFixed  int // 其中靠"根相对"兜底才解析成功的数
	Unresolved int // 趟数耗尽后仍为 uol 的数（含目标缺失）
}

// ResolveUol 就地把所有 uol 节点替换为其引用目标的深拷贝子树。
//
// WZ 语义：uol 的 value 是相对于该 uol 所在父 imgdir 的路径，
// 段之间以 "/" 分隔，支持 ".." 与 "."，例如 blink/0 内的
// "../../default/default" 指向根 imgdir 下 default/default。
// 替换后节点的 Name 保持 uol 自身的 name，Origin 记录原始 value。
//
// 目标不存在（常见于跨 .img 引用）时节点保持 uol 原样，交由上层记录。
func ResolveUol(root *Node) (UolStats, error) {
	if root == nil {
		return UolStats{}, fmt.Errorf("wzxml: root 为 nil")
	}
	parents := map[*Node]*Node{}
	indexParents(root, nil, parents)

	st := UolStats{}
	for pass := 0; pass < MaxUolPass; pass++ {
		pending := collectUols(root)
		if pass == 0 {
			st.Found = len(pending)
		}
		if len(pending) == 0 {
			return st, nil
		}
		progress := 0
		for _, pu := range pending {
			target, err := lookupUolTarget(root, pu.parent, pu.node, parents)
			viaRoot := false
			if err != nil || target == nil || target == pu.node {
				// 实测存在 `default/default` 这类按文件根书写、却未加 ../ 的引用值，
				// 父相对解析不到时再按根相对试一次；父相对优先保证正常语义不变。
				if t := lookupFromRoot(root, pu.node.Value); t != nil && t != pu.node {
					target, err, viaRoot = t, nil, true
				}
			}
			if err != nil || target == nil || target == pu.node {
				continue
			}
			// 先取目标快照再改写，避免目标即自身祖先时读到半改写状态。
			snapshot := target.Clone()
			applyUol(pu.node, snapshot)
			progress++
			st.Resolved++
			if viaRoot {
				st.RootFixed++
			}
		}
		if progress == 0 {
			break
		}
	}
	st.Unresolved = len(collectUols(root))
	return st, nil
}

// lookupFromRoot 把引用值当作从文件根节点出发的路径解析，仅用于父相对失败的兜底。
// 段中的 ".." 在根视角下按"到顶即止"处理，越界不报错。
func lookupFromRoot(root *Node, value string) *Node {
	cur := root
	for _, seg := range strings.Split(strings.TrimSpace(value), "/") {
		seg = strings.TrimSpace(seg)
		switch seg {
		case "", ".", "..":
			continue
		default:
			cur = cur.Child(seg)
			if cur == nil {
				return nil
			}
		}
	}
	if cur == root {
		return nil
	}
	return cur
}

type uolRef struct {
	parent *Node
	node   *Node
}

// Unresolved 是打平后仍指向不到目标的 uol 节点定位信息。
type Unresolved struct {
	Path  string `json:"path"`
	Value string `json:"value"`
}

// ListUnresolved 返回树中类型仍为 uol 的节点，limit <= 0 时返回全部。
// 需要在 ResolveUol 之后调用，Path 为自根起的 name 路径。
func (n *Node) ListUnresolved(limit int) []Unresolved {
	var out []Unresolved
	var rec func(node *Node, prefix string)
	rec = func(node *Node, prefix string) {
		if node == nil || (limit > 0 && len(out) >= limit) {
			return
		}
		path := node.Name
		if prefix != "" {
			path = prefix + "/" + node.Name
		}
		if node.Type == "uol" {
			out = append(out, Unresolved{Path: path, Value: node.Value})
		}
		for _, c := range node.Children {
			rec(c, path)
		}
	}
	rec(n, "")
	return out
}

func collectUols(root *Node) []uolRef {
	var out []uolRef
	root.Walk(func(parent, n *Node) bool {
		if n.Type == "uol" {
			out = append(out, uolRef{parent: parent, node: n})
		}
		return true
	})
	return out
}

func applyUol(dst, src *Node) {
	origin := dst.Value
	dst.Type = src.Type
	dst.Value = src.Value
	dst.Attrs = src.Attrs
	dst.Children = src.Children
	dst.Origin = origin
}

func indexParents(n, parent *Node, out map[*Node]*Node) {
	if n == nil {
		return
	}
	out[n] = parent
	for _, c := range n.Children {
		indexParents(c, n, out)
	}
}

// lookupUolTarget 从 uol 所在父节点出发按段解析路径。
func lookupUolTarget(root, parent, uol *Node, parents map[*Node]*Node) (*Node, error) {
	if parent == nil {
		return nil, fmt.Errorf("wzxml: uol %q 没有父节点", uol.Name)
	}
	segs := strings.Split(strings.TrimSpace(uol.Value), "/")
	cur := parent
	for _, seg := range segs {
		seg = strings.TrimSpace(seg)
		switch seg {
		case "", ".":
			continue
		case "..":
			p, ok := parents[cur]
			if !ok || p == nil {
				return nil, fmt.Errorf("wzxml: uol %q 路径越过根节点: %s", uol.Name, uol.Value)
			}
			cur = p
		default:
			next := cur.Child(seg)
			if next == nil {
				return nil, fmt.Errorf("wzxml: uol 目标缺失 %s (节点 %q)", uol.Value, uol.Name)
			}
			cur = next
		}
	}
	return cur, nil
}

// 以下为本仓库自有的确定性 JSON 序列化，键序固定，便于统计对比与 golden 文件。

// AppendJSON 以紧凑、键序确定的形式把子树追加到 dst。
// 键：_t 类型, _n name, _v value, _o uol 来源, _a 扩展属性, _c 子节点。
func (n *Node) AppendJSON(dst []byte) []byte {
	if n == nil {
		return append(dst, "null"...)
	}
	dst = append(dst, '{')
	dst = appendJSONStr(dst, "_t", n.Type)
	if n.Name != "" {
		dst = append(dst, ',')
		dst = appendJSONStr(dst, "_n", n.Name)
	}
	if n.Value != "" {
		dst = append(dst, ',')
		dst = appendJSONStr(dst, "_v", n.Value)
	}
	if n.Origin != "" {
		dst = append(dst, ',')
		dst = appendJSONStr(dst, "_o", n.Origin)
	}
	if len(n.Attrs) > 0 {
		keys := make([]string, 0, len(n.Attrs))
		for k := range n.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		dst = append(dst, ',')
		dst = append(dst, `"_a":{`...)
		for i, k := range keys {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendJSONStr(dst, k, n.Attrs[k])
		}
		dst = append(dst, '}')
	}
	if len(n.Children) > 0 {
		dst = append(dst, `,"_c":[`...)
		for i, c := range n.Children {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = c.AppendJSON(dst)
		}
		dst = append(dst, ']')
	}
	return append(dst, '}')
}

// JSON 返回子树的确定性 JSON 表示。
func (n *Node) JSON() []byte {
	return n.AppendJSON(nil)
}

func appendJSONStr(dst []byte, k, v string) []byte {
	dst = appendJSONQuote(dst, k)
	dst = append(dst, ':')
	dst = appendJSONQuote(dst, v)
	return dst
}

// appendJSONQuote 按字节写出，保证 WZ 里的非法 UTF-8 序列也能原样归档，
// 不被替换成 U+FFFD 而丢信息。
func appendJSONQuote(dst []byte, s string) []byte {
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			dst = append(dst, `\"`...)
		case '\\':
			dst = append(dst, `\\`...)
		case '\n':
			dst = append(dst, `\n`...)
		case '\r':
			dst = append(dst, `\r`...)
		case '\t':
			dst = append(dst, `\t`...)
		default:
			if c < 0x20 {
				dst = append(dst, `\u00`...)
				dst = append(dst, hexDigits[(c>>4)&0xf], hexDigits[c&0xf])
			} else {
				dst = append(dst, c)
			}
		}
	}
	return append(dst, '"')
}

const hexDigits = "0123456789abcdef"
