package wzxml

import (
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Node 是 WZ 导出 img XML 的同构节点：类型 + name/value + 其余属性 + 子节点。
// WZ 各导出器的属性集不一致（canvas 可能是 width/height，也可能是 format/scale），
// 因此除 name/value 外的属性一律原样保留在 Attrs 中，不做白名单裁剪。
type Node struct {
	Type     string
	Name     string
	Value    string
	Origin   string // uol 解析后记录来源路径，非 uol 节点为空
	Attrs    map[string]string
	Children []*Node
}

var (
	// ErrEmpty 表示文件内没有任何元素节点。
	ErrEmpty = errors.New("wzxml: 文件中没有 XML 元素")
	// ErrMultiRoot 表示根元素之后还存在多余元素。
	ErrMultiRoot = errors.New("wzxml: 存在多个根元素")
)

// Decode 从 r 读取单个根 imgdir 并返回节点树。
func Decode(r io.Reader) (*Node, error) {
	d := xml.NewDecoder(r)
	// WZ 导出文件里存在未定义实体与控制字符，严格模式会直接失败。
	d.Strict = false
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil, ErrEmpty
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		n, err := decodeElement(d, se)
		if err != nil {
			return nil, err
		}
		if err := assertSingleRoot(d); err != nil {
			return nil, err
		}
		return n, nil
	}
}

// DecodeFile 按 UTF-8 读取文件并解码，同时剥离 BOM。
func DecodeFile(path string) (*Node, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	br := bufio.NewReader(f)
	if head, err := br.Peek(3); err == nil && string(head) == "\xef\xbb\xbf" {
		if _, err := br.Discard(3); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	n, err := Decode(br)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return n, nil
}

func assertSingleRoot(d *xml.Decoder) error {
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if _, ok := tok.(xml.StartElement); ok {
			return ErrMultiRoot
		}
		if cd, ok := tok.(xml.CharData); ok && strings.TrimSpace(string(cd)) != "" {
			return ErrMultiRoot
		}
	}
}

func decodeElement(d *xml.Decoder, se xml.StartElement) (*Node, error) {
	n := &Node{
		Type:  se.Name.Local,
		Attrs: make(map[string]string, len(se.Attr)),
	}
	for _, a := range se.Attr {
		switch a.Name.Local {
		case "name":
			n.Name = a.Value
		case "value":
			n.Value = a.Value
		default:
			n.Attrs[a.Name.Local] = a.Value
		}
	}
	if len(n.Attrs) == 0 {
		n.Attrs = nil
	}

	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch tt := tok.(type) {
		case xml.StartElement:
			c, err := decodeElement(d, tt)
			if err != nil {
				return nil, err
			}
			n.Children = append(n.Children, c)
		case xml.EndElement:
			return n, nil
		case xml.CharData:
			// WZ 的值全部承载在属性上，文本节点忽略。
		}
	}
}

// Clone 深拷贝子树。
func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	c := &Node{Type: n.Type, Name: n.Name, Value: n.Value, Origin: n.Origin}
	if n.Attrs != nil {
		c.Attrs = make(map[string]string, len(n.Attrs))
		for k, v := range n.Attrs {
			c.Attrs[k] = v
		}
	}
	if n.Children != nil {
		c.Children = make([]*Node, 0, len(n.Children))
		for _, ch := range n.Children {
			c.Children = append(c.Children, ch.Clone())
		}
	}
	return c
}

// Child 返回第一个 name 匹配的子节点，不存在时返回 nil。
func (n *Node) Child(name string) *Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// ChildAt 返回索引处子节点，越界返回 nil。
func (n *Node) ChildAt(i int) *Node {
	if n == nil || i < 0 || i >= len(n.Children) {
		return nil
	}
	return n.Children[i]
}

// ChildNames 按文档顺序返回全部子节点 name。
func (n *Node) ChildNames() []string {
	if n == nil {
		return nil
	}
	out := make([]string, 0, len(n.Children))
	for _, c := range n.Children {
		out = append(out, c.Name)
	}
	return out
}

// Find 沿 path 逐层查找子节点，任一层缺失返回 nil。
func (n *Node) Find(path ...string) *Node {
	cur := n
	for _, p := range path {
		cur = cur.Child(p)
		if cur == nil {
			return nil
		}
	}
	return cur
}

// Attr 读取扩展属性，name/value 这两个语义字段需通过结构体字段获取。
func (n *Node) Attr(key string) string {
	if n == nil || n.Attrs == nil {
		return ""
	}
	return n.Attrs[key]
}

// StrValue 返回指定 name 的子节点 value，不存在返回空串。
func (n *Node) StrValue(name string) string {
	c := n.Child(name)
	if c == nil {
		return ""
	}
	return c.Value
}

// IntValue 返回指定 name 子节点的整数值。WZ 部分导出器会额外写 hexvalue。
func (n *Node) IntValue(name string) (int64, bool) {
	c := n.Child(name)
	if c == nil {
		return 0, false
	}
	if v, err := strconv.ParseInt(strings.TrimSpace(c.Value), 10, 64); err == nil {
		return v, true
	}
	if hx := c.Attr("hexvalue"); hx != "" {
		if v, err := strconv.ParseInt(strings.TrimSpace(hx), 16, 64); err == nil {
			return v, true
		}
	}
	return 0, false
}

// Walk 前序遍历整棵树；parent 为直接父节点，根节点的 parent 为 nil。
// fn 返回 false 时停止遍历。
func (n *Node) Walk(fn func(parent, node *Node) bool) {
	var rec func(parent, node *Node) bool
	rec = func(parent, node *Node) bool {
		if node == nil {
			return true
		}
		if !fn(parent, node) {
			return false
		}
		for _, c := range node.Children {
			if !rec(node, c) {
				return false
			}
		}
		return true
	}
	rec(nil, n)
}

// Counts 是树的规模统计。
type Counts struct {
	Nodes    int
	MaxDepth int
	ByType   map[string]int
}

// Count 统计节点总数、最大深度与按类型计数。
func (n *Node) Count() Counts {
	c := Counts{ByType: map[string]int{}}
	var rec func(node *Node, depth int)
	rec = func(node *Node, depth int) {
		if node == nil {
			return
		}
		c.Nodes++
		c.ByType[node.Type]++
		if depth > c.MaxDepth {
			c.MaxDepth = depth
		}
		for _, ch := range node.Children {
			rec(ch, depth+1)
		}
	}
	rec(n, 1)
	return c
}

// WriteXML 以单行紧凑形式输出 img XML，属性顺序为 name、value、其余按字典序。
// 用于导出与对照校验，不追求与任意导出器字节级一致。
func (n *Node) WriteXML(w io.Writer) error {
	return writeXMLNode(w, n, 0)
}

func writeXMLNode(w io.Writer, n *Node, depth int) error {
	if n == nil {
		return nil
	}
	if _, err := io.WriteString(w, "<"+n.Type); err != nil {
		return err
	}
	writeAttr := func(k, v string) error {
		_, err := io.WriteString(w, ` `+k+`="`+escapeXML(v)+`"`)
		return err
	}
	if err := writeAttr("name", n.Name); err != nil {
		return err
	}
	if n.Value != "" || n.Type != "imgdir" {
		if err := writeAttr("value", n.Value); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(n.Attrs))
	for k := range n.Attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := writeAttr(k, n.Attrs[k]); err != nil {
			return err
		}
	}
	if len(n.Children) == 0 {
		_, err := io.WriteString(w, "/>")
		return err
	}
	if _, err := io.WriteString(w, ">"); err != nil {
		return err
	}
	for _, c := range n.Children {
		if err := writeXMLNode(w, c, depth+1); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "</"+n.Type+">")
	return err
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
