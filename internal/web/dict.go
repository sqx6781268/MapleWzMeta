// 分类与属性键的中文说明字典。
//
// 只服务于"让筛选面板可读"这一件事：字典没收录的键原样显示成英文键名，
// 检索逻辑完全不依赖这里，加条目不需要改其它代码。
// 键的覆盖面数字来自 store.AttrKeys（真实库内统计），不写死在这里。
package web

import "strings"

// categoryLabels 是分类目录名的中文说明。
// 分类名来自 Character.wz / Item.wz 的目录，与 docs/05-equipment-islot.md 的号段表一致。
var categoryLabels = map[string]string{
	"Cap":             "帽子",
	"Hair":            "发型",
	"Face":            "脸部",
	"Weapon":          "武器",
	"Shield":          "盾牌",
	"Longcoat":        "长外套",
	"Coat":            "上衣",
	"Pants":           "裤子",
	"Shoes":           "鞋子",
	"Glove":           "手套",
	"Cape":            "披风",
	"Ring":            "戒指",
	"Accessory":       "饰品",
	"Pendant":         "吊坠",
	"Belt":            "腰带",
	"Earring":         "耳环",
	"OneHandedWeapon": "单手武器",
	"TwoHandedWeapon": "双手武器",
	"PoleArm":         "长柄武器",
	"Knife":           "短剑",
	"Sword":           "剑",
	"Axe":             "斧",
	"Mace":            "锤",
	"Bow":             "弓",
	"Gun":             "枪",
	"Katara":          "爪",
	"ShadowerBlade":   "暗器",
	"Dragon":          "龙",
	"Taming":          "驯服",
	"TamingMob":       "骑宠",
	"PetEquip":        "宠物装备",
	"Etc":             "其他",
	"Consume":         "消耗",
	"Install":         "安装",
	"Cash":            "商城",
	"Special":         "特殊",
}

// AttrMeta 是一个属性键的中文说明。
// Kind 只影响前端给不给数值输入框：int 允许 ≥/≤，bool 提示填 0/1，text 用等于/包含。
type AttrMeta struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Kind   string `json:"kind"`
	Preset bool   `json:"preset"` // 常用条件，直接出现在侧边栏的快捷筛选里
}

// attrTable 按"过滤时最常用到"排序，不是按库内出现次数（那个来自 /api/meta 的实时统计）。
var attrTable = []AttrMeta{
	{"reqLevel", "需求等级", "int", true},
	{"price", "价格", "int", true},
	{"cash", "商城道具", "bool", true},
	{"islot", "装备槽", "text", true},
	{"vslot", "防具槽", "text", true},
	{"slotMax", "最大堆叠", "int", true},
	{"tuc", "可修复次数", "int", true},
	{"attack", "攻击力", "int", true},
	{"incPAD", "物理攻击", "int", true},
	{"incMAD", "魔法攻击", "int", true},
	{"incPDD", "物理防御", "int", true},
	{"incMDD", "魔法防御", "int", true},
	{"attackSpeed", "攻击速度", "int", true},
	{"durability", "耐久度", "int", true},
	{"reqJob", "需求职业", "int", false},
	{"reqSTR", "需求力量", "int", false},
	{"reqDEX", "需求敏捷", "int", false},
	{"reqINT", "需求智力", "int", false},
	{"reqLUK", "需求运气", "int", false},
	{"speed", "移动速度", "int", false},
	{"incSpeed", "速度加成", "int", false},
	{"undead", "对不死族伤害", "int", false},
	{"incACC", "命中", "int", false},
	{"incEVA", "回避", "int", false},
	{"incMHP", "体力加成", "int", false},
	{"incMMP", "魔力加成", "int", false},
	{"incSTR", "力量加成", "int", false},
	{"incDEX", "敏捷加成", "int", false},
	{"incINT", "智力加成", "int", false},
	{"incLUK", "运气加成", "int", false},
	{"incJump", "跳跃力", "int", false},
	{"tradeBlock", "禁止交易", "bool", false},
	{"equipTradeBlock", "装备后禁止交易", "bool", false},
	{"tradeAvailable", "允许交易", "bool", false},
	{"only", "唯一装备", "bool", false},
	{"onlyEquip", "只能装备一件", "bool", false},
	{"notSale", "不可出售", "bool", false},
	{"exItem", "限时道具", "int", false},
	{"timeLimited", "限时", "bool", false},
	{"quest", "关联任务", "int", false},
	{"mob", "关联怪物", "int", false},
	{"masterLevel", "最大等级", "int", false},
	{"success", "成功率", "int", false},
	{"setItemID", "套装编号", "int", false},
	{"jokerToSetItem", "套装万能件", "int", false},
	{"medalTag", "徽章编号", "int", false},
	{"epicItem", "史诗道具", "bool", false},
	{"cursed", "诅咒", "bool", false},
	{"fixedPotential", "固定潜能", "text", false},
	{"noPotential", "无潜能", "bool", false},
	{"superiorEqp", "高级装备", "bool", false},
	{"royalSpecial", "皇家潜能", "bool", false},
	{"undecomposable", "不可分解", "bool", false},
	{"unchangeable", "不可更改", "bool", false},
	{"accountSharable", "账号可共享", "bool", false},
	{"nameTag", "名字标签", "text", false},
	{"chatBalloon", "对话气泡", "text", false},
	{"type", "类型", "int", false},
	{"grade", "品级", "int", false},
	{"exGrade", "附加品级", "int", false},
	{"specialGrade", "特殊品级", "int", false},
	{"lv", "等级", "int", false},
	{"exp", "经验", "int", false},
	{"reduceReq", "需求降低", "int", false},
	{"recoveryHP", "恢复 HP", "int", false},
	{"recoveryMP", "恢复 MP", "int", false},
	{"limitBreak", "突破上限", "int", false},
	{"knockback", "击退抵抗", "int", false},
	{"bigSize", "大体型", "int", false},
	{"charmEXP", "魅力经验", "int", false},
	{"willEXP", "意志经验", "int", false},
	{"charismaEXP", "感性经验", "int", false},
	{"bossReward", "BOSS 掉落", "bool", false},
	{"collabo", "联动道具", "bool", false},
	{"isDonor", "捐赠者", "bool", false},
	{"monsterBook", "怪物图鉴", "bool", false},
	{"pickUpBlock", "禁止拾取", "bool", false},
	{"noMoveToLocker", "不可移入储物", "bool", false},
	{"pachinko", "弹珠道具", "bool", false},
	{"hybrid", "混合职业", "bool", false},
	{"unitPrice", "单价", "int", false},
	{"incPVPDamage", "PVP 增伤", "int", false},
	{"reqSkillLevel", "需求技能等级", "int", false},
	{"reqSpecJob", "需求转职次数", "int", false},
	{"reqGuildLevel", "需求公会等级", "int", false},
	{"useDelay", "使用延迟", "int", false},
	{"notConsume", "不消耗", "bool", false},
	{"incCriticalMAXDamage", "暴击最大伤害", "int", false},
	{"incAttackCount", "攻击次数增加", "int", false},
	{"stand", "站立动作", "text", false},
	{"walk", "行走动作", "text", false},
	{"sfx", "音效", "text", false},
	{"afterImage", "残影", "text", false},
	{"name", "名称", "text", false},
	{"desc", "描述", "text", false},
}

var attrMetaOf = func() map[string]AttrMeta {
	m := make(map[string]AttrMeta, len(attrTable))
	for _, a := range attrTable {
		m[a.Key] = a
	}
	return m
}()

// attrLabel 返回属性键的中文名；未收录时回落到键名本身。
func attrLabel(key string) string {
	if a, ok := attrMetaOf[key]; ok {
		return a.Label
	}
	return ""
}

// noiseContains 与 noisePrefix 用来屏蔽纯图形/几何属性（icon.width、sample.origin.x…）。
// 这类键占了属性总数的一半以上，放进筛选下拉只会淹没真正有用的条件。
var (
	noiseContains = []string{".origin", ".width", ".height", ".format", ".scale", ".x", ".y", ".top", ".center"}
	noisePrefix   = []string{"icon", "image", "sample", "origin", "lt.", "rb.", "bodyRelMove", "직소"}
)

func isNoiseAttr(k string) bool {
	for _, s := range noiseContains {
		if strings.Contains(k, s) {
			return true
		}
	}
	for _, p := range noisePrefix {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

// categoryLabel 返回分类的中文名；未收录时返回空串，由前端回落到目录名。
func categoryLabel(name string) string {
	return categoryLabels[name]
}
