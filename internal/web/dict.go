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
	"BodySkin":        "身体皮肤",
	"HeadSkin":        "头部皮肤",
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

// attrTableMob / attrTableNPC 是怪物与 NPC 域的属性说明。
//
// 必须分域建表：有些键在物品域里是别的意思——`undead` 在装备上是"对不死族伤害"，
// 在怪物身上是"不死族属性"；`link` 在怪物上是"外观复用哪个怪"。查不到才回落到物品表。
// 条目取自闭包后 /api/meta?kind=mob|npc 的真实高频键，不是照抄 WZ 文档。
var attrTableMob = []AttrMeta{
	{"level", "等级", "int", true},
	{"maxHP", "最大体力", "int", true},
	{"maxMP", "最大魔力", "int", false},
	{"exp", "经验值", "int", true},
	{"PADamage", "物理攻击", "int", false},
	{"MADamage", "魔法攻击", "int", false},
	{"PDDamage", "物理防御", "int", false},
	{"MDDamage", "魔法防御", "int", false},
	{"acc", "命中", "int", false},
	{"eva", "回避", "int", false},
	{"pushed", "击退值", "int", false},
	{"bodyAttack", "身体撞击", "bool", false},
	{"boss", "Boss 怪", "bool", true},
	{"undead", "不死族", "bool", false},
	{"firstAttack", "先制攻击", "int", false},
	{"elemAttr", "元素属性", "text", false},
	{"mobType", "怪物类型", "int", false},
	{"category", "类别", "int", false},
	{"race", "种族", "int", false},
	{"hpRecovery", "体力恢复", "int", false},
	{"mpRecovery", "魔力恢复", "int", false},
	{"PDRate", "物理减伤率", "int", false},
	{"MDRate", "魔法减伤率", "int", false},
	{"summonType", "召唤类型", "int", false},
	{"link", "外观复用", "text", false},
	{"mbookID", "怪物书编号", "int", false},
	{"rareItemDropLevel", "稀有掉落等级", "int", false},
	{"removeAfter", "存在秒数", "int", false},
	{"noregen", "不重生", "bool", false},
	{"invincible", "无敌", "bool", false},
	{"fly", "飞行", "bool", false},
	{"animal", "动物", "bool", false},
	{"derive", "变身形态", "int", false},
	{"chaseSpeed", "追击速度", "int", false},
	{"HPgaugeHide", "隐藏血条", "bool", false},
	{"hpTagColor", "血条颜色", "int", false},
}

var attrTableNPC = []AttrMeta{
	{"func", "功能码", "int", true},
	{"script", "脚本标识", "text", true},
	{"shop", "商店编号", "int", false},
	{"healing", "治疗量", "int", false},
	{"hide", "隐藏", "bool", false},
	{"hideName", "隐藏名字", "bool", false},
	{"dcMark", "传送点标记", "bool", false},
	{"dcLeft", "传送边界左", "int", false},
	{"dcRight", "传送边界右", "int", false},
	{"dcTop", "传送边界上", "int", false},
	{"dcBottom", "传送边界下", "int", false},
	{"forceMove", "强制位移", "bool", false},
	{"float", "漂浮", "bool", false},
	{"imitate", "仿冒外观", "bool", false},
	{"talkMouseOnly", "仅鼠标对话", "bool", false},
	{"trunkPut", "可寄存仓库", "bool", false},
	{"storebank", "可开商店仓库", "bool", false},
	{"noNpcShop", "禁止 NPC 商店", "bool", false},
	{"rpsGame", "猜拳", "bool", false},
	{"parcel", "寄包裹", "bool", false},
	{"guildRank", "可设公会职位", "int", false},
	{"MapleTV", "可上 MapleTV", "bool", false},
}

// attrTableSkill / attrTableMorph / attrTableReactor 是技能、变身、反应堆域的属性说明。
//
// 技能数据没有 `info` 子目录，标量直接挂在技能节点上，等级数值统一带 `level1.` 前缀
// （见 extract.skillEntries）；变身与反应堆没有名称表，键取自各自 info 子目录。
var attrTableSkill = []AttrMeta{
	{"level1.mpCon", "耗蓝", "int", true},
	{"level1.hpCon", "耗血", "int", false},
	{"level1.damage", "伤害", "int", true},
	{"level1.damagepc", "伤害%", "int", false},
	{"level1.mobCount", "攻击个数", "int", true},
	{"level1.time", "持续时间", "int", true},
	{"level1.cooltime", "冷却时间", "int", true},
	{"level1.fixdamage", "固定伤害", "int", false},
	{"level1.speed", "速度变化", "int", false},
	{"level1.pdd", "物理防御变化", "int", false},
	{"level1.mdd", "魔法防御变化", "int", false},
	{"level1.pad", "物理攻击变化", "int", false},
	{"level1.mad", "魔法攻击变化", "int", false},
	{"level1.x", "效果量 X", "int", false},
	{"level1.y", "效果量 Y", "int", false},
	{"mobCode", "关联怪物", "int", false},
	{"invisible", "隐藏", "bool", false},
	{"disable", "已停用", "bool", false},
	{"timeLimited", "限时", "bool", false},
}

var attrTableMorph = []AttrMeta{
	{"speed", "移动速度", "int", true},
	{"jump", "跳跃力", "int", true},
	{"fs", "飞行速度", "int", false},
	{"swim", "游泳速度", "int", false},
}

var attrTableReactor = []AttrMeta{
	{"info", "原始名称（韩文）", "text", false},
}

// attrMetaOfByKind 按域各建一张查找表。
var attrMetaOfByKind = map[string]map[string]AttrMeta{
	"mob":     buildTable(attrTableMob),
	"npc":     buildTable(attrTableNPC),
	"skill":   buildTable(attrTableSkill),
	"morph":   buildTable(attrTableMorph),
	"reactor": buildTable(attrTableReactor),
}

func buildTable(list []AttrMeta) map[string]AttrMeta {
	m := make(map[string]AttrMeta, len(list))
	for _, a := range list {
		m[a.Key] = a
	}
	return m
}

// attrMetaFor 按实体域取属性说明；怪物/NPC 表没有的键回落到物品表口径。
func attrMetaFor(kind, key string) (AttrMeta, bool) {
	if m, ok := attrMetaOfByKind[kind]; ok {
		if a, ok := m[key]; ok {
			return a, true
		}
	}
	a, ok := attrMetaOf[key]
	return a, ok
}

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
