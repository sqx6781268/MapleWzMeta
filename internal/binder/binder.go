package binder

import (
	"sort"

	"github.com/sqx6781268/MapleWzMeta/internal/extract"
)

// Item 是名称与属性关联后的完整视图。
type Item struct {
	ID       string
	Name     string
	Desc     string
	Category string
	Info     map[string]string
	Sources  []string // 命中来源：name / item / character
	HasName  bool
	HasInfo  bool
}

// Complete 表示名称与属性两端都有数据。
func (i Item) Complete() bool { return i.HasName && i.HasInfo }

// Bind 以 8 位物品 ID 为主键合并名称表与属性表。
// 只在一端出现的 ID 也会保留，并通过 HasName/HasInfo 标注缺失方向，
// 便于上层产出"缺名称""缺属性"两张核对清单。
func Bind(names []extract.NameEntry, infos []extract.InfoEntry) []Item {
	index := map[string]*Item{}
	get := func(id string) *Item {
		it, ok := index[id]
		if !ok {
			it = &Item{ID: id}
			index[id] = it
		}
		return it
	}

	for _, n := range names {
		it := get(n.ID)
		if n.Name != "" {
			it.Name = n.Name
			it.HasName = true
		}
		if n.Desc != "" {
			it.Desc = n.Desc
		}
		if n.Category != "" && it.Category == "" {
			it.Category = n.Category
		}
		it.addSource("name")
	}

	for _, in := range infos {
		it := get(in.ID)
		if it.Info == nil {
			it.Info = map[string]string{}
		}
		for k, v := range in.Info {
			it.Info[k] = v
		}
		if in.HasInfoDir {
			it.HasInfo = true
		}
		if it.Category == "" {
			it.Category = in.Category
		}
		it.addSource(sourceTag(in.Source))
	}

	out := make([]Item, 0, len(index))
	for _, it := range index {
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sourceTag(src string) string {
	switch src {
	case "Character.wz":
		return "character"
	case "Item.wz":
		return "item"
	default:
		return src
	}
}

func (i *Item) addSource(s string) {
	for _, have := range i.Sources {
		if have == s {
			return
		}
	}
	i.Sources = append(i.Sources, s)
	sort.Strings(i.Sources)
}

// Stats 是关联结果的分桶计数。
type Stats struct {
	Total     int
	Both      int // 名称与属性齐全
	NameOnly  int // 仅有名称、无属性
	InfoOnly  int // 有属性、无名称（无名道具）
	NoNameIDs []string
}

// Summarize 统计关联覆盖度，NoNameIDs 最多保留 limit 条（limit<=0 表示全部）。
func Summarize(items []Item, limit int) Stats {
	st := Stats{Total: len(items)}
	for _, it := range items {
		switch {
		case it.Complete():
			st.Both++
		case it.HasName && !it.HasInfo:
			st.NameOnly++
		case !it.HasName && it.HasInfo:
			st.InfoOnly++
			if limit <= 0 || len(st.NoNameIDs) < limit {
				st.NoNameIDs = append(st.NoNameIDs, it.ID)
			}
		}
	}
	return st
}
