# 附录 · 实体与 String.wz 的关联

[04-物品ID与关联规则](04-物品ID与关联规则.md) 只讲**物品**这一条链：名称 ↔ 属性怎么对齐。
本附录把视野放大到整个 `String.wz`：**20 张文本表分别属于哪个实体域、ID 长什么形态、
与对应 `.wz` 包的实际关联率是多少**，并顺带记录实体之间互相引用的关系。
现在库里是**物品 / NPC / 怪物**三个实体域各一张表，本附录同时是这条分流域的判据来源。

结论来自本机 `wz/` + `data/mia.db`（语言域 `zh-CN`）实测，分两轮：

- **2026-09-21（修复前历史口径）**：库里只有**物品**一个实体域，名称侧没有表名白名单。
  下文凡标注"历史口径"的数字都属这一轮，**保留是为了能对照**，不是现状。
- **2026-09-22（现状口径）**：接入 `Mob.wz`/`Npc.wz` 两个实体域，并落实 §7 清单里的 1~5 项后重扫 + 清污。

（物品 7/8 位，`String.wz/Eqp` 的发型/脸型另放 5 位；NPC 与怪物 1~8 位皆可，`internal/extract/extract.go:123`）。
两轮都不做 uol 展开；"命中"= 补零到 8 位后能在目标集合里找到。复现命令见 §8。

## 1. String.wz 20 张表总览

`wz/String.wz/` 下每个 `.img.xml` 就是一张文本表。数字键的位数分布决定了它属于哪个实体域。
本表位数按**出现次数**统计（§2 的关联率按**去重后**统计，故个别数字略小，如 `Eqp` 7 位 17,106 → 17,096）。
§1、§2 量的是 `String.wz` 文本自身，与抽取代码无关，**两轮口径都成立**；变化的只有"是否入库、进哪个域"（见 §4.2）：

| 表 | 数字键位数分布 | 主要 string 字段 | 归属实体 | 带 `name`？ |
|---|---|---|---|---|
| `Cash` | 7 位 ×549 | `name` `desc` `msg` | `Item.wz/Cash` | 是（549） |
| `Consume` | 7 位 ×2,429 | `name` `desc` | `Item.wz/Consume` | 是（2,429） |
| `Eqp` | **5 位 ×22,725** + 7 位 ×17,106 | `name` `desc` `itemCollectionName` | `Character.wz/<部位>` | 是（39,820） |
| `Etc` | 7 位 ×2,669 | `name` `desc` | `Item.wz/Etc` | 是（2,669） |
| `Ins` | 7 位 ×710 | `name` `desc` | `Item.wz/Install` | 是（710） |
| `Pet` | 7 位 ×461 | `name` `desc` `descD` | `Item.wz/Pet` | 是（461） |
| `PetDialog` | 7 位 ×454 | `c1` `c2_s1` `f1_f` … | `Item.wz/Pet`（宠物喊话） | **否** |
| `Mob` | 7 位 ×2,002 + 6 位 ×19 + 2 位 ×13 | `name` | `Mob.wz` | 是（2,034） |
| `Npc` | 7 位 ×7,404 + 5 位 ×17 + 4 位 ×13 + 3 位 ×8 + 6 位 ×1 | `n0` `n1` `d0` `d1` `func`，仅 1,842 条有 `name` | `Npc.wz`（对话为主） | 部分（1,842） |
| `Skill` | 7 位 ×360 + 8 位 ×182 + 3 位 ×45 + 4 位 ×26 | `name` `desc` `h1`…`h5` | `Skill.wz`（3/4 位是职业技能书分组） | 是（542） |
| `Map` | 9 位 ×5,531 + 7 位 ×20 + 5 位 ×13 + 1 位 ×5 | `mapName` `streetName` `mapDesc` | `Map.wz/Map` | **否** |
| `MonsterBook` | 7 位 ×333 + 6 位 ×11 | `episode`（子节点 `map`/`reward` 为 int） | `Mob.wz` | **否** |
| `ToolTipHelp` | 1 位 ×1,837 + 9 位 ×738 + 2 位 ×69 + 5 位 ×18 + 7 位 ×8 | `Title` `Desc` `in00` `market00` | `Map.wz` + UI 对象 | **否** |
| `GLcloneC` | 1 位 ×5 | `Class` `Desc` `StartMap` `Equip` | 职业克隆（`Base.wz`） | 否 |
| `EULA` `GuestEULA` `TestEULA` `TrialEULA` | 无数字键 | `Text00`…`Text73` | 纯条款文案 | 否 |
| `NameChange` `TransferWorld` | 无数字键 | `Text00`…`Text31` | 纯系统文案 | 否 |

三条判据值得记住：

   `Mob`/`Npc`/`Skill` 三张属别的实体域。这九张现在**按表名白名单分流**（`internal/extract/extract.go:83`），
   六张物品表 → item 表、`Mob` → mob 表、`Npc` → npc 表；`Skill` 未登记（技能不是本项目的检索对象），
   与其余 11 张表一起整份跳过。修复前三张别域表曾整体灌进物品域，造成 4,347 条污染（**历史口径**，见 §4）。
   被 `extract.Names` 的"`name` 必需"判据（`internal/extract/extract.go:179`）挡掉；
   现在它们也不在白名单里，双重不入库。
   现已对 `Eqp` 单独放行 5 位（`internal/extract/extract.go:135`，见 §5）。

## 2. 文本表 → 实体包的关联率

| 关联 | 分子/分母 | 覆盖率 | 未命中样例 |
|---|---|---|---|
| `String/Map` 9 位 → `Map.wz/Map` 文件名 | 4,243 / 5,394 | 78.7% | `701100000`（未导出的地图） |
| `String/Npc` 7 位 → `Npc.wz` 文件名 | 7,385 / 7,404 | 99.7% | `1012120`、`9000016` |
| `String/Skill` 8 位 → `Skill.wz` 内层 ID | 181 / 182 | 99.5% | `10000013` |
| `String/Skill` 7 位 → `Skill.wz` 内层 ID | 353 / 360 | 98.1% | `09001003`~`09001007` |
| `String/MonsterBook` 7 位 → `Mob.wz` | 333 / 333 | 100% | — |
| `String/Mob` 7 位 → `Mob.wz` | 1,853 / 2,002 | 92.6% | `8220016`~`8220020`（只有文本、无怪物包） |
| `String/Mob` 6 位 → `Mob.wz` | 19 / 19 | 100% | — |
| `String/Npc` 4 位 → `Npc.wz` | 13 / 13 | 100% | — |
| `String/Etc` 7 位 → `Item.wz`+`Character.wz` | 2,613 / 2,669 | 97.9% | `04000463` |
| `String/Consume` → 同上 | 2,357 / 2,429 | 97.0% | `02049116` |
| `String/Eqp` 7 位 → 同上 | 16,807 / 17,096 | 98.3% | `01012459` |
| `String/Ins` → 同上 | 708 / 710 | 99.7% | `03010071` |
| `String/Cash` → 同上 | 484 / 549 | 88.2% | `05340000`（`Item.wz/Cash/0534.img.xml` 根本没导出） |
| `String/Pet` 7 位 → `Item.wz/Pet` 文件名 | **306 / 461** | 66.4% | 该目录实测只有 357 个文件，461−306=155 条宠物名称在属性侧根本没有文件 |
| **`String/Eqp` 5 位 → 库内 Hair/Face 行** | **22,691 / 22,725** | 99.9% | 修复前名称侧**从未写入**；现全部补回，见 §5 |

> 注 1：上面的"→ Item/Character"分母是**属性侧文件里的内层数字 imgdir 集合**。唯一的例外曾是
> `Item.wz/Pet`（文件名即 ID、没有内层键，见 §3），所以 `String/Pet` 走"文件名"这一列才是 100%——
> 这条在修复后依然成立，变化是 Pet 的**属性**也从文件名这条路取到了（此前整目录漏抽）。
> 注 2（**修复前历史口径**，2026-09-21）：`Cash`/`Eqp`/`Consume`/`Etc` 的未命中项**是真实的属性导出缺口**，
> 不是统计口径问题——抽查 `01012459`、`05340000` 均为 `info={}`、`hasInfo=false`。把 4,733 条缺属性行按名称来源拆开
> （同一行可被多表命中，故有重叠）：`Mob` 1,971 + `Npc` 1,428 + `Skill` 491 属当时污染顺带造出的
> "只有名字的行"、`Pet` 461 是 §3 的抽取缺陷、`Eqp` 286（Weapon 143 / PetEquip 59 / Taming 43 …）
> 与 `Consume` 72、`Cash` 65、`Etc` 56 才是属性侧真缺口。
> 修复后这三类行各归其域、Pet 属性入库，物品域"仅有名称"（`kind=item` 且 `has_name=1 AND has_info=0`）
> 从 4,733 降到 **670**（= 有名称 46,628 − 齐全 45,958）。
> 另有 **3,791 行物品 + 2 行怪物**是 `has_name=0 且 has_info=0` 的空壳行（正好等于 §4 一次性清污抹掉的名称行数，
> 即"名字被清、属性本来也没有"的旧缺陷产物），已按"无名 + 无属性 + 无任何 `kind:id` 溯源认领"三条件删除，
> 所以物品总行数 56,349 → **52,558**、怪物 2,534 → 2,532。真实实体行不可能同时满足这三条：被解析到就必然有名称或属性并被文件认领。
> 而 `Skill` 那 491 条**不再是污染、也不再是缺口**：技能文本表至今不在白名单里，技能 ID 不再写进任何行。


## 3. 实体包侧的命名形态差异

关联之所以容易断，根因是各包**把 ID 放在不同位置**：

| 包 | 一个实体 = | ID 位置 | 位数 |
|---|---|---|---|
| `Mob.wz` | 一个文件 | 文件名 = 根 imgdir 名 | 7 位（`0000021.img.xml`，文本表里同一怪物写作 2 位键 `21`） |
| `Npc.wz` | 一个文件 | 文件名 = 根 imgdir 名 | 7 位 |
| `Reactor.wz` / `Morph.wz`(4 位) / `TamingMob.wz` | 一个文件 | 文件名 | 各自位长 |
| `Map.wz/Map/<区>/<id>.img.xml` | 一个文件 | 文件名 | 9 位 |
| `Item.wz/<类别>/<组>.img.xml` | 文件内层 imgdir | **内层键** | 8 位（如 `0430.img.xml` 内挂 `04300001`） |
| `Item.wz/Pet/<id>.img.xml` | 一个文件 | 文件名 = 根 imgdir 名 | 7 位（`5000000.img.xml`） |
| `Character.wz/<部位>/<id>.img.xml` | 一个文件 | 文件名 = 根 imgdir 名 | 8 位 |
| `Character.wz/<id>.img.xml`（根级 18 个） | 一个文件 | 同上 | 8 位，**无类别目录**（现按 `info.islot` 归 `BodySkin` 9 / `HeadSkin` 9） |
| `Skill.wz/<职业码>.img.xml` | 文件内层 imgdir | 内层键 | 7/8 位 |
| `Quest.wz` | `0XXXX.img.xml` 内层 | 内层键 | 4~6 位 |

`Item.wz/Pet` 是 `Item.wz` 里唯一的例外（其余 5 个子目录 `Cash`/`Consume`/`Etc`/`Install`/`Special` 都是分组文件）。

（当时在 `internal/extract/extract.go:117`），`Item.wz/Pet` 走不到那条分支，于是宠物**只有名称、没有属性**——
现在 `extract.RootID`（`internal/extract/extract.go:328`）改成**要求文件名与根 imgdir 名同为数字 ID 且位数 ≥ 7**
（`internal/extract/extract.go:334`），不再按包名开特判，于是
`Mob.wz` / `Npc.wz` / `Item.wz/Pet` / `Character.wz` 四条路径全部走这条路（`internal/extract/extract.go:231`），
而 `Item.wz/Etc/0430.img.xml` 这类**根名同为数字、但只有 4 位**的分组文件仍被位数门槛挡在外面、继续走内层键。
判据必须同时看"文件名 == 根 imgdir 名"，只看位数的话分组文件会被当成单实体、把整文件属性挂错 ID。
实测现状：`query -cat Pet` 有命中，`query -id 05000000` = 褐色小猫**且带属性**。

实体包规模（用于对照；2026-09-22 重新数过）：`Mob.wz` 顶层 2,380 文件（2,379 个 7 位怪物包 + 1 个非数字名）
外加 `Mob.wz/QuestCountGroup/` 10 个查表文件（`find` 全量 2,389）、`Npc.wz` 7,506（无子目录）、`Item.wz/Pet` 357、`Reactor.wz` 472、
`Morph.wz` 71、`TamingMob.wz` 7、`Map.wz/Map` 9 位 5,691、`Quest.wz` 数字条目 3,199、`Skill.wz` 内层 7/8 位 535。
本轮新纳入扫描的是 `Mob.wz` 2,380 + `Npc.wz` 7,506 等（§7 收益里的 +9,895 即由此而来）；
`Mob.wz/QuestCountGroup/*.img.xml` 的文件名同样是 7 位怪物号，但内容是"怪物 → 任务计数"查表，
**不是怪物本体**：怪物/NPC 只认紧贴包根的那一层文件，判据在 `internal/extract/extract.go` 的 `Infos`（本轮一度把这 10 个
文件当怪物属性收了进来，使怪物"有属性"虚高到 2,385，加规则挡掉后回滚到 2,379）。

## 4. 名称侧到底写入了什么

### 4.1 修复前：一张表灌三个实体域（**历史口径**，2026-09-21）

用**当时**的判据（imgdir 名为 7/8 位纯数字 + 直接子节点有 `string name="name"`）逐表统计，
可完整解释 `docs/09` §6.1 记的那 28,262 条名称条目（**历史口径**）：

| 表 | 贡献条目 | 当时语义 |
|---|---|---|
| `Eqp` 7 位 | 17,095 | ✅ 真物品名称 |
| `Consume` | 2,429 | ✅ |
| `Etc` | 2,669 | ✅ |
| `Mob` 7 位 | 2,002 | ❌ 怪物名灌进物品 |
| `Npc` 7 位（带 name） | 1,803 | ❌ NPC 名灌进物品 |
| `Ins` | 710 | ✅ |
| `Skill` 7+8 位 | 542 | ❌ 技能名灌进物品 |
| `Cash` | 549 | ✅ |
| `Pet` | 461 | ✅ |
| **合计** | **28,260** | 污染 4,347 条 = **15.4%** |

（28,260 与当时 scan 实际入库 28,262 相差 2，来自个别跨行排版条目。）

**污染面是 100%，不是"约 4,165"**。三张表的键补零后，与当时库内 56,264 个物品 ID 逐一比对：

| 表 | 带 name 的 7/8 位键 | 与物品 ID 重叠 |
|---|---|---|
| `Mob` | 2,002 | **2,002（100%）** |
| `Npc` | 1,803 | **1,803（100%）** |
| `Skill` | 542 | **542（100%）** |
| `Pet`（对照组，正确） | 461 | 461（100%） |

也就是说**每一条怪物/NPC/技能名称都落在某个物品行上**——物品 ID 空间被各实体域整体复用，
不存在"错开号段"的保护。当时的实证：

```
query -id 01110100  →  绿蘑菇        # String/Mob 1110100
query -id 08220016  →  堕落的飞龙    # String/Mob 8220016，该 ID 在 Mob.wz 里根本没有怪物包
```

根因是分流只看路径前缀：相对路径以 `String.wz` 开头就整份文件按名称侧处理，没有表名白名单。

### 4.2 现状：按表名白名单分流（2026-09-22）
白名单是一张 `表名 → 实体域` 的静态映射（`internal/extract/extract.go:83`），
（`internal/scan/scan.go:327`），**不在表里的整份文件跳过、不产名称条目**：

| 表 | 去处 | 现状条目 |
|---|---|---|
| `Cash` / `Consume` / `Eqp` / `Etc` / `Ins` / `Pet` | `item` 表 | 物品 46,628 行 `has_name=1`（88.72%） |
| `Mob` | `mob` 表 | 怪物 2,034 行有名称（80.33%）——与 §1"带 `name` 2,034"完全吻合 |
| `Npc` | `npc` 表 | NPC 1,842 行有名称（24.65%）——与 §1"只有 1,842 条带 `name`"吻合，其余只有对话键 `n0`/`d0`，被"`name` 必需"判据挡在门外 |
| `Skill` 及其余 11 张（`Map`/`MonsterBook`/`PetDialog`/`ToolTipHelp`/`GLcloneC`/4 张 `EULA`/`NameChange`/`TransferWorld`） | 不入库 | 0 |
`Names` 命中 ID 节点后**不再向下递归**（`internal/extract/extract.go:158`）：怪物/NPC 条目内部
有 `0`、`1` 这类数字子节点（动画帧、对话分支），继续下钻会把它们当成新实体。

（每批落库前显式 `sort`：`internal/scan/scan.go:178`、`:184`）——历史上那三次重扫恰好稳定，
但文件数或机器负载一变就会翻转，所以这条必须和 #1 一起做（§7 建议 #4）。

**旧残留要一次性清掉**：白名单只管住"以后不再写入"，历史上灌进 `item` 表的怪物/NPC 名还在。
判据是"物品域 `has_name=1`、但没有任何名称侧文件的 `item:<id>` 溯源认领"（认领关系即
`wz_file.ids`，管理页 `/api/admin/sources` 用的同一份数据），本机实清 **3,791 行**，复查残留 0。
清理后 §4.1 那两条实证各自归位：

```
query -id 01110100            → 物品域无名称（旧值"绿蘑菇"）
query -kind mob -id 01110100  → 绿蘑菇            # 名称回到怪物域
query -id 08220016            → 无名称            # Mob.wz 里没有这个怪物包，怪物域也不该有它
```

## 5. Hair/Face 缺名称：位数门槛已放开

### 5.1 诊断（**历史口径**，2026-09-21）

`docs/09` 旧版的"已知缺口"（现在是 §5）曾把"缺名称 28,568 条、Hair+Face 占 87%"定性为**导出范围问题**。实测**证伪**：

| 事实 | 数字 |
|---|---|
| `String.wz/Eqp.img.xml` 里 5 位键的分组归属 | 只有 `Hair`(13,366) 与 `Face`(9,359) 两组，合计 22,725 |
| 这些键补零到 8 位后存在于 `Character.wz/Hair`+`Face` 文件名 | 22,690 / 22,725 |
| 这些键补零后命中当时库内 `has_name=0` 的行 | **22,691**（Hair 13,348 + Face 9,343） |
| 其中已经带属性的比例 | **22,691 / 22,691 = 100%**（`has_info=1`） |
| 当时全库 `has_name=0` 总数 | 28,568，且 100% 都有属性 |

即：**只要放行 5 位 ID，`名称+属性齐全` 会从 22,963 涨到约 45,654（+22,691）**
——这个预测已经兑现，实测落在 **45,958**（§5.2 的表）。

`NormalizeID`（`internal/extract/extract.go:33`）本来就支持 5 位补零，门槛只在识别层。

### 5.2 现状：门槛按域下沉到 `LooksLikeID`

`extract.LooksLikeID(kind, table, raw)`（`internal/extract/extract.go:123`）：
- 物品：7/8 位；**`table == "Eqp"` 时另放 5 位**（`internal/extract/extract.go:135`）。
  放行只挂在 `Eqp` 这一张表上，因为 5 位键在 `String.wz` 里只有 Hair/Face 两组（5.1 第一行），
  换成全局放宽就会顺手把 `Mob` 的 6 位、`Npc` 的 3~5 位键也放进来。
- NPC / 怪物：1~8 位皆可——`Mob.wz/0000021.img.xml` 与 `String.wz/Mob` 里的 `21` 是同一个怪物，
  两侧都得认，位数没有统一上界可用。

实测收益（`kind=item`，`zh-CN`，重扫 + 清污后）：

| 指标 | 修复前 | 现状 |
|---|---|---|
| 有名称 | 27,696 | **46,628（88.72%）** |
| 名称+属性齐全 | 22,963 | **45,958（87.44%）** |
| 缺名称 | 28,568 | **5,930** |

Hair/Face 的名称已补回：抽样 `00020000` = 酷-男脸(黑色)（Face，5 位放行的直接产物）。

**剩下的才是真缺口**：把未被 5 位键覆盖的无名称行拿去和 `String.wz` **全部 20 张表的任意数字键**
比对，命中 **0** 条（含 `name` 的键也命中 0）。当时的分布是
`Weapon` 2,185 / `Hair` 1,886 / `Accessory` 407 / `Cap` 300 / `Face` 212 / `Longcoat` 157 /
`Install` 155 / `Shoes` 110 / `TamingMob` 101 / `Etc` 81 / 其余更少，合计 5,877 —— 这些是**只有美术导出、文本未导出**的物品，
与现状"缺名称 5,930 行"**完全对上**（当时的 5,877 是旧数据集口径；本轮新增的 85 个物品行都带名称，空壳行已删）。

## 6. 其他关联性（非 String.wz）

实体之间还有几条真实、可用的引用链，目前**没做成关联查询**（§7 第 6 项未实施）。
和上一版的区别是：`mob`/`npc` 两张表已经入库，所以这些链的**落点已经存在**——
例如库内 `info.mob` 的 353 个取值 100% 能在 `mob` 表里查到行，只差把它渲染成一条跳转链接。
链本身如下：

| 引用链 | 载体 | 实测 |
|---|---|---|
| Map → Mob | `Map.wz/Map/**/*.img.xml` 的 `<imgdir name="life"><imgdir name="N"><string name="type" value="m"/><string name="id" value="1110100"/>` | 5,692 个地图文件中 2,736 个含 life；`type=m` 出现 27,509 次、`n` 3,012 次；去重后引用 877 个怪物 ID，**877/877 在 `Mob.wz`**，866 个有文本 |
| Map → Npc | 同上，`type=n` | 去重 1,212 个，**1,211 在 `Npc.wz`**，1,211 有文本 |
| Map → 怪物分布（反向） | 由上可聚合成"怪物 X 出现在哪些地图" | 未实现 |
| Etc → Npc | `Etc.wz/NpcLocation.img.xml` 的 8 位键 | 1,695 条，**100% 命中 `Npc.wz`**（NPC 与地图的关联表） |
| Etc → Item | `Etc.wz/ItemMake.img.xml` 的配方产物 ID | 引用 834 个，**100% 命中 Item/Character**（合成/配方链） |
| Mob ↔ MonsterBook | `String.wz/MonsterBook.img.xml` 按怪物 ID 挂 `episode`/`map`/`reward` | 333/333 命中 `Mob.wz`（怪物书=怪物分布+奖励） |
| Item → Mob | 库内 `info.mob`（召唤类道具/戒指挂怪物） | 353 个去重取值，**100% 命中 `Mob.wz`** |
| Item → Quest | 库内 `info.questId` | 27 个取值，仅 9 个在 `Quest.wz` 数字条目里（**弱关联，别依赖**） |
| Item → Quest | 库内 `info.quest` | 1,184 条，取值恒为 `1` —— 它是**布尔标志**（任务道具），不是引用 |
| Item → 职业 | 库内 `info.reqJob` | 19,587 条，去重 15 个取值，只有 `0`~`4` 是职业码，其余是位掩码组合 |
| Item → Map | `String.wz/ToolTipHelp.img.xml` 的 9 位键 | 738 个，指向地图物件说明（未导出对应实体） |
| 图标域 → 实体域 | `imgdata/<域>/<id>.img.png` | 见下 |

### 图标域的 ID 串味（已修）

`imgdata/` 三个域共用同一个 8 位 ID 空间：`Character` 20,681 张、`Npc` 7,465 张、`Item` 357 张。
**旧实现**（历史口径）把 `icons.Build` 定成"按 `Item`=0 > `Character`=1 > 其他=5 的优先级跨域去重"，
得 28,307 个"可用 ID"（`Character` 20,676 / `Item` 357 / `Npc` 7,274，191 张 Npc 图被装备图挤掉），
再拿这一把 ID 去 join 库内 56,264 个物品行：

| 指标 | 值（历史口径） |
|---|---|
| 物品行命中图标 | 22,162 |
| 其中图标来自 `Character` | 20,278 |
| 其中来自 `Item` | 306 |
| **其中其实来自 `Npc`（拿 NPC 立绘当物品图标）** | **1,578（7.1%）** |

取样 `00002003`、`00012000`、`09000001`、`09000002`、`09001000` 当时都会显示一张 NPC 图。
串味能发生的根因在 ID 层面：`Npc.wz` 的 7,506 个 ID 与物品 ID 重叠 **1,806**、`Mob.wz` 的 2,379 个重叠 **1,861**。
**现状**：索引改成 `(域, ID) → 文件` 的二维结构（`internal/icons/icons.go:44`），
（`internal/icons/icons.go:34`：`item` → `Item`+`Character`、`npc` → `Npc`、`mob` → `Mob`），
查询一律带域（`entry`/`Has`/`Path`，`internal/icons/icons.go:142`、`:173`、`:142`），
`rankOf`（`internal/icons/icons.go:120`）**只在同域内**给同号多张图定序、跨域不再互抢。
HTTP 侧 `/img/<id>.png?for=<域>`（`internal/icons/icons.go:186`），
列表接口只在**非物品域**时给 URL 追加 `?for=`（`internal/web/web.go:142`，物品域留空即按 `item` 处理，老链接不受影响）。

| 口径（2026-09-22） | 值 |
|---|---|
| 入索引文件 | 28,503（`Character`=20,681 / `Npc`=7,465 / `Item`=357；`find` 磁盘口径 28,504，多的 1 个是 `01102488_.img.png`，词干含下划线被拒） |
| 跨域合并可用 ID | 28,307（仍由 `stats`/`WarmLog` 打印，仅为兼容旧统计，**不代表某个实体能取到图**） |
| 按域可取图 ID | item=**21,033**、npc=**7,465**、mob=**0** |
| 行带**本域**图标 | 物品 20,635（36.62%）、NPC 7,418（99.25%）、怪物 **0** |

`mob=0` 不是索引坏了，而是 **`imgdata/` 里根本没有 `Mob` 目录**——本数据集从没导出过怪物图，
所以怪物列表整列无图标是数据事实。要补图得回上游用 wzimgget 加导 `Mob` 域（见 [10-图标提取与wzimgget联动](10-图标提取与wzimgget联动.md)）。

旧口径"22,165 行带图标"里有 1,578 行是 NPC 立绘，现已不再挂到物品行上；
复发判据是 `docs/09` §4 那四条 curl（物品域查 Npc 图必须 404、`?for=npc` 必须 200）。

## 7. 处置清单：1~5 项已实施，第 6 项未动

| # | 动作 | 状态 | 落点（`包/文件.go:行号`） | 实测收益 |
|---|---|---|---|---|
| 1 | 名称侧加**表名白名单**，按表分流到三个实体域 | ✅ 已实施 | `internal/extract/extract.go:83`、`:98`；闸门在 `internal/scan/scan.go:327` | 新写入侧污染归零；旧残留一次性清 **3,791 行**（判据见 §4.2），复查残留 0 |
| 2 | 名称侧位数门槛按域放宽（`Eqp` 开 5 位、NPC/怪物 1~8 位） | ✅ 已实施 | `internal/extract/extract.go:123`、`:135` | Hair/Face 一次性补回约 22,691 条名称；物品有名称 27,696 → **46,628**（与 #1 的 −3,791 清污叠加后的净值 **+18,932**）。怪物侧另得 2,034 条名称、NPC 侧 1,842 条 |
| 3 | "根名即 ID"的特判从 `Character.wz` 扩到任意源 | ✅ 已实施 | `internal/extract/extract.go:328`（判据 `:334`），调用点 `:231` | `Item.wz/Pet` **357 个文件全部入库**（`query -cat Pet` 由 0 条变为 357 条，其中 306 条同时有名称）；旧文档写的"461 条"是 `String/Pet` 的**名称**条目数，不是属性文件数 |
| 4 | 落库顺序与到达顺序解耦 | ✅ 已实施 | `internal/scan/scan.go:178`、`:184` | 同 ID 多文件命中时胜者确定（按 `ID` 再按来源路径），重扫不再抖动 |
| 5 | 图标索引带上来源域 | ✅ 已实施 | `internal/icons/icons.go:34`、`:44`、`:150`；URL 侧 `internal/web/web.go:142` | 物品行不再出现 NPC 立绘（历史上 1,578 行）；行带本域图标：物品 20,635 / NPC 7,418 / 怪物 0 |
| 6 | 利用 §6 的引用链（怪物分布、配方、NPC 位置、怪物书） | ❌ 未实施 | — | 需新表与新抽取器，属功能扩展而非修缺 |

存储层按域分表 `item`/`npc`/`mob`（`internal/store/store.go:63`，DDL 见 `:138`），
行归属由 `scan` 从路径推导（`internal/extract/extract.go:105`）、存储层不再自己判路径；
HTTP 全线支持 `?kind=`（`internal/web/web.go:149`，陌生值直接 400）；
属性中文说明字典按域分表（`internal/web/dict.go:172`、`:211`、`:237`）——
`undead` 在装备上是"对不死族伤害"、在怪物身上是"不死族属性"，共用一张表必错。

配套影响：`docs/09` §2 的规模基线、§4 的图标基线、§5/§6 的缺口与缺陷条目**已随本轮改写**；
本附录凡未标"历史口径"的数字都是 2026-09-22 的复测值。

## 8. 复现取数命令

一次性脚本（**修复前**那轮的数字都由这 8 段跑出，放在 `tmp/` 下、跑完可删；`node:sqlite` 需 `--experimental-sqlite`）：

```bash
node --experimental-sqlite tmp/wzrel3.mjs   # 20 张文本表的位数分布与 string 字段频次
node --experimental-sqlite tmp/wzrel.mjs    # 文本表→实体包覆盖率、Item/Character 内层条目、Eqp 5 位键缺口
node --experimental-sqlite tmp/wzrel2.mjs   # Map.life / Etc.NpcLocation / Etc.ItemMake / Quest 交叉引用
node --experimental-sqlite tmp/wzrel4.mjs   # Eqp 5 位键 ↔ 库内 Hair/Face 行 ↔ Character.wz 文件名
node --experimental-sqlite tmp/wzrel5.mjs   # 图标域串味 + Mob/Npc ID 与物品 ID 的重叠面（串味已修，脚本仍是跨域口径）
node --experimental-sqlite tmp/wzrel6.mjs   # 库内 info.mob / questId / reqJob 取值 → 实体包命中率
node --experimental-sqlite tmp/wzrel7.mjs   # Mob/Npc/Skill 三表 name 键与库内物品 ID 的重叠（§4.1）
node --experimental-sqlite tmp/wzrel8.mjs   # 4,733 条缺属性行按名称来源表拆开（§2 注 2，历史口径）
```

修复后的三个实体域规模 / 图标覆盖用 `stats` 直接读，不必跑脚本（`scan` 每次收尾也会打印同一份）：

```bash
./bin/maplewzmeta.exe stats        # 物品 / NPC / 怪物 各一行 + 分类 Top12 + 图标按域可取图 ID
```

清污那 3,791 行是**一次性手工 SQL**（未留脚本），判据固定为：
物品域 `has_name=1` 的行，其 `id` 在 `wz_file.ids` 里**没有任何名称侧文件的 `item:<id>` 认领**。
反查单个 ID 用 `/api/admin/sources?kind=item&id=<id>`，来源列表里没有 `String.wz/*` 即为无主。

纯命令行核对（不需要 node）。Git Bash 里 perl 正则**别写 `(?!…)`**，`!` 会被改写成 `\` 而报
`Sequence (?\...) not recognized`；下面几条都是本机可直接粘贴执行的：

```bash
# 每张文本表带 name 字段的条目数（对照 §1 最后一列）
for f in wz/String.wz/*.xml; do
  printf '%-16s %s\n' "$(basename $f .img.xml)" "$(grep -o '<string name="name"' $f | wc -l)"
done

# 名称侧识别面**现在按域而定**，别再用一条 7/8 位的 grep 概括三域：
#   物品：`String.wz/{Cash,Consume,Eqp,Etc,Ins,Pet}` 的 7/8 位键 + Eqp 的 5 位键
#   怪物 / NPC：`String.wz/{Mob,Npc}` 的任意位数数字键（1~8 位都认）
for f in wz/String.wz/{Mob,Npc}.img.xml; do
  printf '%-10s 任意位数数字键=%-7s 其中带name=%s\n' "$(basename $f .img.xml)" \
    "$(grep -o '<imgdir name="[0-9]\+"' $f | wc -l)" \
    "$(grep -o '<string name="name"' $f | wc -l)"
done

# Eqp 的 5 位键只出现在 Hair / Face 两组（输出 Face: 9359、Hair: 13366）——放行 5 位的依据就是这条
perl -0777 -ne 'while (/<imgdir name="(Hair|Face|Accessory|Cap|Cape|Coat|Dragon|Glove|Longcoat|Pants|PetEquip|Ring|Shield|Shoes|Weapon)">|<imgdir name="(\d{5})"/g) { if (defined $1) { $g = $1 } else { $n{$g}++ } } print "$_: $n{$_}\n" for sort keys %n' wz/String.wz/Eqp.img.xml
```

抽样复核（2026-09-22 现状，逐条都对得上；`query` 用 `-kind` 切实体域，缺省即 item）：

```bash
./bin/maplewzmeta.exe query -id 01110100              # 物品域已无名称（旧值"绿蘑菇"）
./bin/maplewzmeta.exe query -kind mob -id 01110100    # 怪物域 = 绿蘑菇
./bin/maplewzmeta.exe query -id 04000000              # 蓝色蜗牛壳（旧值"集中术"= Skill 表污染）
./bin/maplewzmeta.exe query -id 08220016              # 无名称：Mob.wz 里根本没有这个怪物包
./bin/maplewzmeta.exe query -id 00020000              # 酷-男脸(黑色)：Face，5 位放行的产物
./bin/maplewzmeta.exe query -id 05000000              # 褐色小猫 **且带属性**：Item.wz/Pet 根名即 ID
./bin/maplewzmeta.exe query -kind npc -id 09000000    # 珀尔
./bin/maplewzmeta.exe query -kind mob -id 00000021    # 僵尸蘑菇：String/Mob 里写作 2 位键 `21`
./bin/maplewzmeta.exe query -cat Pet                  # 有命中（修复前 0 条）
```
