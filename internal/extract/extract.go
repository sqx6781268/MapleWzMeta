// Package extract 把解码后的 WZ 节点树抽取成可按 ID 关联的记录。
//
// 三个实体域共用同一套解析管线，只是来源与位数门槛不同：
//   - 物品（item）：名称 `String.wz/{Cash,Consume,Eqp,Etc,Ins,Pet}.img.xml`，属性 `Item.wz` + `Character.wz`；
//   - NPC（npc）：名称 `String.wz/Npc.img.xml`，属性 `Npc.wz/<id>.img.xml`；
//   - 怪物（mob）：名称 `String.wz/Mob.img.xml`，属性 `Mob.wz/<id>.img.xml`。
//
// 名称与属性物理分离、只靠数字 ID 关联，各源把 ID 放在不同层级：
//   - String.wz/{Cash,Consume,Ins,Pet,Mob,Npc}.img.xml：根 imgdir 直接挂 ID；
//   - String.wz/Etc.img.xml：根下多一层 "Etc" 再挂 ID；
//   - String.wz/Eqp.img.xml：根下是 "Eqp" 再按部位（Accessory 等）分层，然后挂 ID；
//   - Item.wz/<类别>/<组>.img.xml：一个文件挂多个 ID，ID 是文件内层 imgdir；
//   - Item.wz/Pet、Character.wz、Mob.wz、Npc.wz：一个文件就是一个实体，根 imgdir 名即 ID。
//
// 因此识别 ID 不依赖固定层数，而是"imgdir 名在该实体域的位数形态内"，
// 并统一零填充到 8 位，才能跨源对齐（docs/附录 §3 记了每一处的实测形态）。
package extract

import (
	"path"
	"path/filepath"
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

// Kind 是实体域。三个域各自入库，ID 空间互不合并——
// 实测 Mob.wz/Npc.wz 的 ID 与物品 ID 重叠 1,861 / 1,806 个（docs/附录 §6），
// 混在一张表里就是 4,347 条名称污染。
type Kind string

const (
	KindItem    Kind = "item"
	KindNPC     Kind = "npc"
	KindMob     Kind = "mob"
	KindSkill   Kind = "skill"
	KindMorph   Kind = "morph"
	KindReactor Kind = "reactor"
)

// Kinds 是受支持的实体域，顺序即界面与管理页的展示顺序。
var Kinds = []Kind{KindItem, KindNPC, KindMob, KindSkill, KindMorph, KindReactor}

// ValidKind 判定字符串是否为已知实体域（空串按 item 处理，保持向后兼容）。
func ValidKind(s string) bool {
	if s == "" {
		return true
	}
	for _, k := range Kinds {
		if Kind(s) == k {
			return true
		}
	}
	return false
}

// nameTables 是 `String.wz` 表名 → 实体域的白名单。
//
// 没列进来的表整份跳过。判据来自实测：`String.wz` 20 张表里只有 9 张带 `name` 子节点，
// 其中 `Mob`/`Npc`/`Skill` 三张属别的实体域，它们的键补零后与库内物品 ID **100% 重叠**，
// 不设白名单就会把别的实体的名字写到物品行上（docs/附录 §4、§7 建议 #1）。
// `Map`/`MonsterBook`/`PetDialog`/`ToolTipHelp` 目前不入库：地图与图鉴不是本项目的检索对象。
//
// `Morph`/`Reactor` 没有名称来源（实测 Morph.wz 71 个文件无一含 `name`，
// 地图数据里的 reactor 节点是空壳），所以只登记属性侧，名称一律留给"缺名称"展示。
var nameTables = map[string]Kind{
	"Cash": KindItem, "Consume": KindItem, "Eqp": KindItem, "Etc": KindItem, "Ins": KindItem, "Pet": KindItem,
	"Mob": KindMob, "Npc": KindNPC, "Skill": KindSkill,
}

// StringTable 从 `String.wz/Mob.img.xml` 取出表名 `Mob`；不是 String.wz 下的表时返回空。
func StringTable(rel string) string {
	rest, ok := strings.CutPrefix(filepath.ToSlash(rel), "String.wz/")
	if !ok {
		return ""
	}
	return strings.TrimSuffix(rest, ".img.xml")
}

// NameKindOf 判定一个名称侧文件是否属于白名单，并给出实体域。
func NameKindOf(rel string) (Kind, bool) {
	k, ok := nameTables[StringTable(rel)]
	return k, ok
}

// KindOf 判定属性侧文件属于哪个实体域：包名直接决定。
// 名称侧不走这里——它由 `String.wz` 的表名决定，见 `NameKindOf`。
func KindOf(rel string) Kind {
	rel = filepath.ToSlash(rel)
	switch sourceOf(rel) {
	case "Mob.wz":
		return KindMob
	case "Npc.wz":
		return KindNPC
	case "Skill.wz":
		return KindSkill
	case "Morph.wz":
		return KindMorph
	case "Reactor.wz":
		return KindReactor
	}
	return KindItem
}

// LooksLikeID 按实体域判断 imgdir 名能否当 ID 用。
//
//   - 物品：7 或 8 位；但 `String.wz/Eqp` 的发型/脸型条目用 **5 位** ID，
//     实测 22,691 条 Hair/Face 的名称就因此被整批丢弃（docs/附录 §5），故对 Eqp 单独放行；
//   - NPC / 怪物 / 技能 / 变身 / 反应堆：1~8 位皆可。`Mob.wz`、`Npc.wz` 的文件名一律是补零到 7 位，
//     而 `String.wz/Mob` 里同一怪物写成 `21`（僵尸蘑菇 = `Mob.wz/0000021.img.xml`），
//     两侧都要认，位数没有统一上界可用；`Morph.wz` 更短（`0001.img.xml`，4 位）。
func LooksLikeID(k Kind, table, raw string) bool {
	id := strings.TrimSuffix(strings.TrimSpace(raw), ".img")
	if !isDigits(id) {
		return false
	}
	switch k {
	case KindNPC, KindMob, KindSkill, KindMorph, KindReactor:
		return len(id) <= IDLen
	default:
		if len(id) == 7 || len(id) == IDLen {
			return true
		}
		return table == "Eqp" && len(id) == 5
	}
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

// Names 收集一张 `String.wz` 文本表里的名称/描述条目；不在白名单里的表整份跳过。
//
// 命中的 ID 节点**不再向下递归**：怪物/NPC 条目内部会出现 `0`、`1` 这样的数字子节点
// （动画帧、对话分支），继续下钻就把它们当成了新实体。
func Names(rel string, root *wzxml.Node) []NameEntry {
	kind, ok := NameKindOf(rel)
	if !ok || root == nil {
		return nil
	}
	table := StringTable(rel)
	var out []NameEntry
	var rec func(n *wzxml.Node)
	rec = func(n *wzxml.Node) {
		for _, c := range n.Children {
			if c.Type != "imgdir" {
				continue
			}
			if LooksLikeID(kind, table, c.Name) {
				name := c.Child("name")
				if name == nil || name.Type != "string" {
					continue // 是实体但没文本（如 NPC 只有对话），不产出行
				}
				out = append(out, nameEntry(c, NormalizeID(c.Name), categoryOf(n), name.Value))
				continue
			}
			rec(c)
		}
	}
	rec(root)
	return out
}

func nameEntry(n *wzxml.Node, id, category, name string) NameEntry {
	e := NameEntry{ID: id, Name: name, Category: category}
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
	return e
}

// InfoEntry 是一个实体的属性集合，来自 Item.wz / Character.wz / Mob.wz / Npc.wz。
type InfoEntry struct {
	ID         string
	Kind       Kind
	Category   string
	Source     string
	Info       map[string]string
	Parts      []string // info 之外的子节点名（外观动画组等），用于判断数据完整度
	HasInfoDir bool
}

// Infos 收集一个 img 文件里的属性条目。
// rel 形如 "Item.wz/Etc/0430.img.xml"、"Character.wz/Cap/01002936.img.xml" 或 "Mob.wz/0000021.img.xml"。
func Infos(rel string, root *wzxml.Node) []InfoEntry {
	if root == nil {
		return nil
	}
	rel = filepath.ToSlash(rel)
	kind := KindOf(rel)
	source := sourceOf(rel)
	category := categoryOfDir(rel)

	// 怪物/NPC 的实体文件必须紧贴包根目录。实测 `Mob.wz/QuestCountGroup/0210100.img.xml`
	// 这类子目录是"怪物号 → 任务计数"的附属查表，文件名同样是 7 位怪物号，
	// 按单文件实体解析就会把查表值写成怪物属性。
	if kind != KindItem && category != "" {
		return nil
	}

	// 技能是"组文件再套一层"：`Skill.wz/000.img.xml` → `skill/<7 位技能 ID>`。
	// 技能节点没有 `info` 子目录，核心数值在 `level/1`，所以单独走一条解析路径。
	if kind == KindSkill {
		if out := skillEntries(root, category, source); len(out) > 0 {
			return out
		}
	}

	// 一个文件就是一个实体：Character.wz、Mob.wz、Npc.wz 与 Item.wz/Pet 都是这种形态。
	if id := RootID(rel, root); id != "" {
		return []InfoEntry{entryFromInfo(root, id, kind, category, source)}
	}

	var out []InfoEntry
	for _, c := range root.Children {
		if c.Type != "imgdir" || !LooksLikeID(kind, "", c.Name) {
			continue
		}
		out = append(out, entryFromInfo(c, NormalizeID(c.Name), kind, category, source))
	}
	return out
}

func entryFromInfo(node *wzxml.Node, id string, kind Kind, category, source string) InfoEntry {
	e := InfoEntry{ID: id, Kind: kind, Category: category, Source: source, Parts: node.ChildNames()}
	info := node.Child("info")
	if info == nil {
		return e
	}
	e.HasInfoDir = true
	e.Info = flattenScalars(info)
	e.Category = rootCategory(source, category, e.Info)
	return e
}

// skillEntries 解析 `Skill.wz/<组>.img.xml`：技能 ID 在 `skill/` 再下一层，
// 且技能节点没有 `info` 子目录——标量属性直接挂在节点上，等级数值在 `level/<等级>`。
// 这里**只收标量**，跳过 icon/effect 等画布，免得画布坐标把属性表冲成噪声。
func skillEntries(root *wzxml.Node, category, source string) []InfoEntry {
	grp := root.Child("skill")
	if grp == nil {
		return nil
	}
	var out []InfoEntry
	for _, c := range grp.Children {
		if c.Type != "imgdir" || !LooksLikeID(KindSkill, "", c.Name) {
			continue
		}
		e := InfoEntry{
			ID: NormalizeID(c.Name), Kind: KindSkill, Category: category, Source: source,
			Parts: c.ChildNames(), Info: scalarChildren(c),
		}
		// 等级数值（耗蓝 mpCon、伤害、冷却 cooltime 等）在 level/1，加前缀避免与顶层键撞名。
		if lv := c.Child("level"); lv != nil {
			if one := lv.Child("1"); one != nil {
				for k, v := range scalarChildren(one) {
					e.Info["level1."+k] = v
				}
			}
		}
		if len(e.Info) > 0 {
			e.HasInfoDir = true
		}
		out = append(out, e)
	}
	return out
}

// scalarChildren 只取 imgdir 下的标量节点（int/string/float 等），跳过 imgdir/canvas/vector。
func scalarChildren(n *wzxml.Node) map[string]string {
	out := map[string]string{}
	if n == nil {
		return out
	}
	for _, c := range n.Children {
		switch c.Type {
		case "imgdir", "canvas", "vector":
			continue
		default:
			out[c.Name] = c.Value
		}
	}
	return out
}

// rootCategory 给 `Character.wz` 根级文件补分类：那批文件没有类别目录，按目录取名会得到空串。
//
// 判据用 `info.islot` 而不是 ID 前缀——实测根级 18 个文件（`00002000`~`00002011`、
// `00012000`~`00012011`）的 islot 恰好只有 `Bd` 与 `Hd` 两种，且全库只有这 18 行用这两个槽位：
// `Bd` 是身体皮肤（画布名 `body`），`Hd` 是纸娃娃基础头部（画布名 `head`）。
func rootCategory(source, category string, info map[string]string) string {
	if source != "Character.wz" || category != "" {
		return category
	}
	switch info["islot"] {
	case "Bd":
		return "BodySkin"
	case "Hd":
		return "HeadSkin"
	}
	return category
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
// 分层时返回分层目录名，如 Eqp/Accessory、Etc。父节点自身是数字 ID 时也返回空。
func categoryOf(parent *wzxml.Node) string {
	if parent == nil || strings.HasSuffix(parent.Name, ".img") {
		return ""
	}
	if isDigits(parent.Name) {
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

// RootID 在"一个文件即一个实体"时返回其 ID，否则返回空串。
//
// 判据是**文件名与根 imgdir 名同为数字 ID**，而不是只看位数：
//   - `Character.wz/Cap/01002936.img.xml`（8 位）、`Mob.wz/0000021.img.xml`、
//     `Npc.wz/0000700.img.xml`、`Item.wz/Pet/5000000.img.xml`（7 位）都成立；
//   - `Item.wz/Etc/0430.img.xml` 这类分组文件根名也是数字（`0430.img`），
//     但只有 4 位，被位数门槛挡掉，仍走"文件内层 imgdir"那条路；
//   - `Morph.wz/0001.img.xml` 的实体号本身就只有 4 位，是**唯一**放宽门槛的包
//     （该目录下全是一文件一实体，没有组文件，放宽不会误判）；
//   - `Skill.wz/000.img.xml` 的组号只有 3 位，恒不成立，技能走 `skill/` 下探那条路。
//
// 旧实现只对 `Character.wz` 开特判，导致 461 个宠物道具属性整批漏抽（docs/附录 §3）。
func RootID(rel string, root *wzxml.Node) string {
	if root == nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	base := strings.TrimSuffix(path.Base(rel), ".xml")
	base = strings.TrimSuffix(base, ".img")
	minLen := 7
	if sourceOf(rel) == "Morph.wz" {
		minLen = 4
	}
	if !isDigits(base) || len(base) < minLen {
		return ""
	}
	if strings.TrimSuffix(root.Name, ".img") != base {
		return ""
	}
	return NormalizeID(base)
}

// IsNumeric 供上层做数据校验。
func IsNumeric(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}
