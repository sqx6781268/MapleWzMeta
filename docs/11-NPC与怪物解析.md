# 11 · NPC 与怪物解析

本文专写 **NPC（`npc`）** 与 **怪物（`mob`）** 两个实体域：数据从哪来、ID 长什么样、
为什么这么分表、图标现状、以及常用属性键的中文口径。物品域的规则见
[04-物品ID与关联规则](04-物品ID与关联规则.md)，通用存储/接口见 [06](06-数据模型与存储规则.md)、[08](08-命令行与查询接口.md)。
结论均为本机 `wz/` + `data/mia.db`（语言域 `zh-CN`）实测；跨域重叠、位数分布的完整底稿见
[附录-实体与String.wz的关联](附录-实体与String.wz的关联.md)。

## 1. 两域的数据来源

| 侧 | 来源 | 抽取函数 | 说明 |
|---|---|---|---|
| 名称 / 描述 | `String.wz/Npc.img.xml`、`String.wz/Mob.img.xml` 的 `name` / `desc` | `extract.Names`（`internal/extract/extract.go:77`） | 由 `nameTables`（`internal/extract/extract.go:77`）把 `Npc`→`npc`、`Mob`→`mob` |
| 属性（info） | `Npc.wz/<id>.img.xml`、`Mob.wz/<id>.img.xml`，**一文件一实体** | `extract.Infos`（`internal/extract/extract.go:330`）+ `RootID`（`internal/extract/extract.go:330`） | 域由包名定：`KindOf`（`internal/extract/extract.go:99`） |

口径说明：名称侧只认 `name` / `desc` 两个字段，与外部后台（BeiDou gms-server 的"信息查询"只读这两个字段）
对齐——NPC 条目里大量存在的 `n0`/`n1`/`d0`/`d1` 这类**对话文本**当前**不入名称表**（它们是 `NameEntry.Extra`，
只在内存视图里，不写库，见 [04](04-物品ID与关联规则.md) §3）。

## 2. 一个文件即一个实体

`Mob.wz` / `Npc.wz` 与 `Character.wz` 一样是"一文件一实体"：`Mob.wz/0000021.img.xml` 就是一个怪物。
判据由泛化后的 `RootID` 给出——**文件名（去 `.xml`/`.img`）与根 imgdir 名同为数字 ID 且位数 ≥7** 即成立
（修复前只对 `Character.wz` 开特判，导致 `Item.wz/Pet` 的 357 个宠物文件属性整批漏抽，见 [附录](附录-实体与String.wz的关联.md) §3）。
`extract.Names` 命中 ID 节点后**不再向下递归**：怪物/NPC 条目内部有 `0`/`1` 这类数字子节点
（动画帧、对话分支），继续下钻会把它们当成新实体。

> **怪物与 NPC 只认紧贴包根的那一层文件**。`Mob.wz/QuestCountGroup/*.img.xml` 的文件名同样是 7 位怪物号
> （实测 10 个），但内容是"怪物 → 任务计数"的查表，按单文件实体解析就会把查表值写成怪物属性——
> 本轮一度收了进来，使怪物"有属性"从 2,379 虚高到 2,385，加规则挡掉后回滚清理。
> 判据在 `extract.Infos`：非物品域时 `categoryOfDir(rel) != ""` 直接不出条目。

## 3. ID 位数形态与归一

| 侧 | 原生位数（实测） | 归一后 |
|---|---|---|
| `Mob.wz` / `Npc.wz` 文件名 | 一律 7 位（`0000021`） | `NormalizeID` 补零到 8 位 `00000021` |
| `String.wz/Mob` 键 | 7 位为主，另有 6 位、2 位（同一怪物 `21`） | 补零到 8 位，与文件名对齐 |
| `String.wz/Npc` 键 | 7 位为主，另有 5/4/3 位 | 补零到 8 位 |
两域识别门槛是 **1~8 位纯数字皆可**（`LooksLikeID`，`internal/extract/extract.go:117` 的 `KindNPC`/`KindMob` 分支），
不设上界——因为 `Mob.wz` 文件名一律 7 位、而 `String.wz/Mob` 里同一怪物可能写成 2 位，两侧都要认，
再按数值补零到 8 位归一。检索侧的 `id` / `q`（纯数字）也统一走 `NormalizeID`（见 [04](04-物品ID与关联规则.md) §8），
所以怪物原生 7 位、手输 `1110100` 能命中库里的 `01110100`。

## 4. 为什么 NPC 只有 1,842 条有名称、怪物有 2,034

`internal/extract/extract.go:158`）：没有 `name` 的条目即便有数字键也不产出行。两域的文本表结构不同：

- **怪物**：`String.wz/Mob` 几乎每个数字键都直接挂 `name`（实测 2,034 条带 `name`，[附录](附录-实体与String.wz的关联.md) §1）。
- **NPC**：`String.wz/Npc` 里多数条目是**对话 NPC**，挂的是 `n0`/`n1`/`d0`/`d1`/`func` 而非 `name`，
  只有 1,842 条带 `name`（[附录](附录-实体与String.wz的关联.md) §1）。

所以"有名称"的 NPC 少于怪物，是**数据事实**、不是解析缺陷。属性侧则相反：
`Npc.wz` 有 7,506 个文件、`Mob.wz` 2,379 个数字 ID（[附录](附录-实体与String.wz的关联.md) §3），
即大量 NPC 有立绘与属性、却没有文本名——它们在 `npc` 表里表现为"有属性、缺名称"的行，
用 `query -kind npc -unnamed` 或页面"仅有属性（缺名称）"可筛出。

## 5. 图标现状（按实体域）
`entityDomains`（`internal/icons/icons.go:34`）把 NPC 限定在 `Npc` 目录、怪物限定在 `Mob` 目录，
物品只认 `{Item, Character}`。实测：

| 域 | `imgdata` 内是否有对应目录 | 该域能拿到图标的行数 |
|---|---|---|
| item | `Item`(357) + `Character`(20,682) 文件 | 22,162（含来自 Character 的装备图，[附录](附录-实体与String.wz的关联.md) §6） |
| npc | `Npc` 7,465 张图 | **7,418 行有图**（口径：`npc` 表内 `icons.Has("npc", id)` 为真的行数） |
| mob | **无 `Mob` 目录** | **0**——`imgdata` 目前没导出怪物图，怪物域整域无图 |
图标 URL 物品域之外的都带 `?for=<kind>`（`toView`，`internal/web/web.go:131`），保证 NPC 行取 `Npc` 目录、
不会串到装备图。怪物域要出图，需 [wzimgget](https://github.com/sqx6781268/wzimgget) 补一个 `imgdata/Mob/` 目录，
届时 `entityDomains["mob"]` 已经预留好映射，无需改代码。

## 6. 跨域同 ID 重叠与分表结论

物品 / NPC / 怪物的 ID 补零到 8 位后**整体落在同一个号段空间**，同一条 `01110100` 既是"绿蘑菇戒指"（物品）
也是"绿色水灵菇"（怪物）。实测重叠面（口径：各域数字键补零后与库内物品 ID 逐一比对，
[附录](附录-实体与String.wz的关联.md) §4、§6）：

| 域 | 与物品 ID 重叠 |
|---|---|
| 怪物 `Mob` | 2,002 / 2,002（**100%**） |
| NPC `Npc` | 1,803 / 1,803（**100%**） |
| 立绘包 `Npc.wz`/`Mob.wz` ID | 与物品 ID 分别重叠 1,806 / 1,861 |

因此三个域**必须分表**（`item`/`npc`/`mob` 三张列结构完全一样的表，见 [06](06-数据模型与存储规则.md) §2）。
配套的两道防线：

1. **名称侧按表名白名单定域**：`String.wz/Mob`、`/Npc` 的名字归各自域，修复前"以 `String.wz` 开头就算名称侧"
   造成 4,347 条跨实体污染，现已归零。
2. **溯源与清理按 `kind:id` 判归属**：`wz_file.ids` 每项带域前缀（`mob:00000021`），
   `ClearSide`/`EditedIDs`/`SourcesOf` 都按"域 + ID"操作（[06](06-数据模型与存储规则.md) §3.2），
   清怪物的失效贡献绝不会碰到同 ID 的物品行。

## 7. 常用属性键与中文说明

物品表 `attrTable` 仅作全局兜底（`attrMetaFor`，`internal/web/dict.go:251`）。
**分域的必要性**：同一个键在两个域语义不同——`undead` 在装备上是"对不死族伤害"、在怪物上是"不死族属性"；
`link` 在怪物上是"外观复用哪个怪"。下列取字面键名（非中文），括号为中文说明。

### 怪物（`Mob.wz` 的 `info`）

| 键 | 中文 | 键 | 中文 |
|---|---|---|---|
| `level` | 等级 | `exp` | 经验值 |
| `maxHP` | 最大体力 | `maxMP` | 最大魔力 |
| `PADamage` | 物理攻击 | `MADamage` | 魔法攻击 |
| `PDDamage` | 物理防御 | `MDDamage` | 魔法防御 |
| `acc` / `eva` | 命中 / 回避 | `pushed` | 击退值 |
| `boss` | Boss 怪（bool） | `undead` | 不死族（bool，非装备域语义） |
| `elemAttr` | 元素属性 | `mobType` | 怪物类型 |
| `PDRate` / `MDRate` | 物理 / 魔法减伤率 | `hpRecovery` / `mpRecovery` | 体力 / 魔力恢复 |
| `link` | 外观复用哪个怪 | `mbookID` | 怪物书编号 |
| `firstAttack` | 先制攻击 | `bodyAttack` | 身体撞击 |
| `rareItemDropLevel` | 稀有掉落等级 | `removeAfter` | 存在秒数 |
| `noregen` / `invincible` / `fly` / `animal` | 不重生 / 无敌 / 飞行 / 动物 | | |

### NPC（`Npc.wz` 的 `info`）

| 键 | 中文 | 键 | 中文 |
|---|---|---|---|
| `func` | 功能码 | `script` | 脚本标识 |
| `shop` | 商店编号 | `healing` | 治疗量 |
| `dcMark` | 传送点标记 | `dcLeft`/`dcRight`/`dcTop`/`dcBottom` | 传送边界（左右上下） |
| `hide` / `hideName` | 隐藏 / 隐藏名字 | `forceMove` | 强制位移 |
| `float` | 漂浮 | `imitate` | 仿冒外观 |
| `talkMouseOnly` | 仅鼠标对话 | `rpsGame` | 猜拳 |
| `trunkPut` | 可寄存仓库 | `storebank` | 可开商店仓库 |
| `noNpcShop` | 禁止 NPC 商店 | `parcel` | 寄包裹 |
| `guildRank` | 可设公会职位 | `MapleTV` | 可上 MapleTV |

> 表中列的是**高频、常被过滤**的键；完整清单以 `/api/meta?kind=…` 的实时统计为准，
> 未收录的键原样显示英文键名、按原名可查。

## 8. 检索与接口速记

| 用法 | 命令 / 请求 |
|---|---|
| CLI 查怪物 | `maplewzmeta query -kind mob -q 僵尸蘑菇` |
| CLI 查 NPC 缺名称 | `maplewzmeta query -kind npc -unnamed` |
| 列表 HTTP | `GET /api/items?kind=mob&q=蘑菇` |
| 单条 HTTP | `GET /api/item?kind=npc&id=1012100`（`id` 自动补零） |
| 元数据字典 | `GET /api/meta?kind=mob`（属性中文说明走怪物域表） |
| 图标 | `GET /img/01110100.png?for=mob`（缺省即物品域；怪物域当前 0 图） |
| 页面切换 | 顶栏「实体」下拉切 NPC / 怪物，分类筛选块在这两个域自动隐藏 |
