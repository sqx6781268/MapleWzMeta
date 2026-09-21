# 附录 · 实体与 String.wz 的关联

[04-物品ID与关联规则](04-物品ID与关联规则.md) 只讲**物品**这一条链：名称 ↔ 属性怎么对齐。
本附录把视野放大到整个 `String.wz`：**20 张文本表分别属于哪个实体域、ID 长什么形态、
与对应 `.wz` 包的实际关联率是多少**，并顺带记录实体之间互相引用的关系。

结论全部来自本机 `wz/` + `data/mia.db`（语言域 `zh-CN`）实测，测量日期 **2026-09-21**。
口径统一为：**只按 `<imgdir name="数字">` 抓条目，不做 uol 展开**；"命中"= 补零到 8 位后能在目标集合里找到。
复现命令见 §8。

## 1. String.wz 20 张表总览

`wz/String.wz/` 下每个 `.img.xml` 就是一张文本表。数字键的位数分布决定了它属于哪个实体域。
本表位数按**出现次数**统计（§2 的关联率按**去重后**统计，故个别数字略小，如 `Eqp` 7 位 17,106 → 17,096）：

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

1. **只有 9 张表带 `name`**：`Cash`/`Consume`/`Eqp`/`Etc`/`Ins`/`Pet` 六张是物品文本表，
   `Mob`/`Npc`/`Skill` 三张属别的实体域——它们是目前名称污染的全部来源（§4、§5）。
2. `Map`/`MonsterBook`/`PetDialog`/`ToolTipHelp` 虽然也用数字键，但**没有 `name` 子节点**，
   被 `extract.Names` 的"`name` 必需"判据天然挡掉，无害。
3. `Eqp` 一张表里混了**两种位数**：7 位是普通装备、5 位是发型/脸型。后者整批被丢弃（§5）。

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
| `String/Pet` 7 位 → `Item.wz/Pet` 文件名 | 461 / 461 | 100% | — |
| **`String/Eqp` 5 位 → 库内 Hair/Face 行** | **22,691 / 22,725** | 99.9% | 名称侧**从未写入**，见 §5 |

> 注 1：上面的"→ Item/Character"分母是**属性侧文件里的内层数字 imgdir 集合**。唯一的例外是
> `Item.wz/Pet`（文件名即 ID、没有内层键，见 §3），所以 `String/Pet` 走"文件名"这一列才是 100%。
> 注 2：`Cash`/`Eqp`/`Consume`/`Etc` 的未命中项**是真实的属性导出缺口**，不是统计口径问题——
> 抽查 `01012459`、`05340000` 均为 `info={}`、`hasInfo=false`。把 4,733 条缺属性行按名称来源拆开
> （同一行可被多表命中，故有重叠）：`Mob` 1,971 + `Npc` 1,428 + `Skill` 491 属 §4 污染顺带造出的
> "只有名字的行"、`Pet` 461 是 §3 的抽取缺陷、`Eqp` 286（Weapon 143 / PetEquip 59 / Taming 43 …）
> 与 `Consume` 72、`Cash` 65、`Etc` 56 才是属性侧真缺口。


## 3. 实体包侧的命名形态差异

关联之所以容易断，根因是各包**把 ID 放在不同位置**：

| 包 | 一个实体 = | ID 位置 | 位数 |
|---|---|---|---|
| `Mob.wz` | 一个文件 | 文件名 | 7 位（未补零） |
| `Npc.wz` | 一个文件 | 文件名 | 7 位 |
| `Reactor.wz` / `Morph.wz`(4 位) / `TamingMob.wz` | 一个文件 | 文件名 | 各自位长 |
| `Map.wz/Map/<区>/<id>.img.xml` | 一个文件 | 文件名 | 9 位 |
| `Item.wz/<类别>/<组>.img.xml` | 文件内层 imgdir | **内层键** | 8 位（如 `0430.img.xml` 内挂 `04300001`） |
| **`Item.wz/Pet/<id>.img.xml`** | **一个文件** | 文件名 = 根 imgdir 名 | **7 位**（`5000000.img.xml`） |
| `Character.wz/<部位>/<id>.img.xml` | 一个文件 | 文件名 = 根 imgdir 名 | 8 位 |
| `Character.wz/<id>.img.xml`（根级 18 个） | 一个文件 | 同上 | 8 位，**无类别目录** |
| `Skill.wz/<职业码>.img.xml` | 文件内层 imgdir | 内层键 | 7/8 位 |
| `Quest.wz` | `0XXXX.img.xml` 内层 | 内层键 | 4~6 位 |

`Item.wz/Pet` 是 `Item.wz` 里唯一的例外（其余 5 个子目录 `Cash`/`Consume`/`Etc`/`Install`/`Special` 都是分组文件）。
当前 `extract.Infos` 只对 `Character.wz` 开了"根名即 ID"的特判（`internal/extract/extract.go:117`），
`Item.wz/Pet` 走不到这条分支，于是 461 个宠物道具**只有名称、没有属性**——
实测 `query -cat Pet` 命中 0 条，`05000000`（快动作）与 `05000001`（褐色小狗）均 `info={}`。
现成的 `extract.RootID`（`internal/extract/extract.go:206`）已经能从这类路径认出 8 位 ID，但目前**无人调用**。

实体包规模（用于对照）：`Mob.wz` 2,385 文件 / 2,379 个数字 ID、`Npc.wz` 7,506、`Reactor.wz` 472、
`Morph.wz` 71、`TamingMob.wz` 7、`Map.wz/Map` 9 位 5,691、`Quest.wz` 数字条目 3,199、`Skill.wz` 内层 7/8 位 535。

## 4. 名称侧到底写入了什么

用 `extract.Names` 的判据（imgdir 名为 7/8 位纯数字 + 直接子节点有 `string name="name"`）逐表统计，
可完整解释 `docs/09` §5 的 28,262 条名称条目：

| 表 | 贡献条目 | 语义 |
|---|---|---|
| `Eqp` 7 位 | 17,095 | ✅ 真物品名称 |
| `Consume` | 2,429 | ✅ |
| `Etc` | 2,669 | ✅ |
| `Mob` 7 位 | 2,002 | ❌ 怪物名 |
| `Npc` 7 位（带 name） | 1,803 | ❌ NPC 名 |
| `Ins` | 710 | ✅ |
| `Skill` 7+8 位 | 542 | ❌ 技能名 |
| `Cash` | 549 | ✅ |
| `Pet` | 461 | ✅ |
| **合计** | **28,260** | 污染 4,347 条 = **15.4%** |

（28,260 与 scan 实际入库 28,262 相差 2，来自个别跨行排版条目。）

关键修正：**污染面是 100%，不是"约 4,165"**。三张表的键补零后，与库内 56,264 个物品 ID 逐一比对：

| 表 | 带 name 的 7/8 位键 | 与物品 ID 重叠 |
|---|---|---|
| `Mob` | 2,002 | **2,002（100%）** |
| `Npc` | 1,803 | **1,803（100%）** |
| `Skill` | 542 | **542（100%）** |
| `Pet`（对照组，正确） | 461 | 461（100%） |

也就是说**每一条怪物/NPC/技能名称都落在某个物品行上**——物品 ID 空间被各实体域整体复用，
不存在"错开号段"的保护。实证：

```
query -id 01110100  →  绿蘑菇        # String/Mob 1110100
query -id 08220016  →  堕落的飞龙    # String/Mob 8220016，该 ID 在 Mob.wz 里根本没有怪物包
```

根因是分流只看路径前缀：相对路径以 `String.wz` 开头就整份文件按名称侧处理
（`internal/scan/scan.go:240`），没有表名白名单。

## 5. Hair/Face 缺名称：不是导出问题，是位数门槛

`docs/09` §4 旧版把"缺名称 28,568 条、Hair+Face 占 87%"定性为**导出范围问题**。实测**证伪**：

| 事实 | 数字 |
|---|---|
| `String.wz/Eqp.img.xml` 里 5 位键的分组归属 | 只有 `Hair`(13,366) 与 `Face`(9,359) 两组，合计 22,725 |
| 这些键补零到 8 位后存在于 `Character.wz/Hair`+`Face` 文件名 | 22,690 / 22,725 |
| 这些键补零后命中库内 `has_name=0` 的行 | **22,691**（Hair 13,348 + Face 9,343） |
| 其中已经带属性的比例 | **22,691 / 22,691 = 100%**（`has_info=1`） |
| 全库 `has_name=0` 总数 | 28,568，且 100% 都有属性 |

即：**只要放行 5 位 ID，`名称+属性齐全` 会从 22,963 直接涨到约 45,654（+22,691）**。

拦路的判据是 `LooksLikeItemID` 的长度检查（`internal/extract/extract.go:39-42`：只接受 7 或 8 位），
`Names` 在 `internal/extract/extract.go:70` 据此整批跳过。
`NormalizeID`（`:27`）本身是支持 5 位补零的，门槛只在识别层。

**剩下的 5,877 条才是真缺口**：把未被 5 位键覆盖的无名称行拿去和 `String.wz` **全部 20 张表的任意数字键**
比对，命中 **0** 条（含 `name` 的键也命中 0）。分布：
`Weapon` 2,185 / `Hair` 1,886 / `Accessory` 407 / `Cap` 300 / `Face` 212 / `Longcoat` 157 /
`Install` 155 / `Shoes` 110 / `TamingMob` 101 / `Etc` 81 / 其余更少。这些是**只有美术导出、文本未导出**的物品。

## 6. 其他关联性（非 String.wz）

实体之间还有几条真实、可用但目前完全没用上的引用链：

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

### 图标域的 ID 串味

`imgdata/` 三个域共用同一个 8 位 ID 空间：`Character` 20,682 张、`Npc` 7,465 张、`Item` 357 张；
`icons.Build` 按 `Item`=0 > `Character`=1 > 其他=5 的优先级去重后得 28,307 个可用 ID
（`Character` 20,676 / `Item` 357 / `Npc` 7,274，即 191 张 Npc 图被装备图挤掉）。

拿这 28,307 个 ID 去 join 库内 56,264 个物品行：

| 指标 | 值 |
|---|---|
| 物品行命中图标 | 22,162 |
| 其中图标来自 `Character` | 20,278 |
| 其中来自 `Item` | 306 |
| **其中其实来自 `Npc`（拿 NPC 立绘当物品图标）** | **1,578（7.1%）** |

取样：`00002003`、`00012000`、`09000001`、`09000002`、`09001000` 等行都会显示一张 NPC 图。
同源问题在 ID 层面也能量化：`Npc.wz` 的 7,506 个 ID 与库内物品 ID 重叠 **1,806**，
`Mob.wz` 的 2,379 个重叠 **1,861**。

因此**图标域不能当作"这张图属于物品"的证据**，它只是一个 ID→文件的索引；
未来若要严格化，应像 §7 建议的那样把来源域记进索引并在展示层标注或过滤。

## 7. 处置建议（按性价比排序，均未实施）

| # | 动作 | 收益 | 代价 |
|---|---|---|---|
| 1 | 名称侧加**表名白名单**：只认 `Cash/Consume/Eqp/Etc/Ins/Pet` 六张表 | 直接消掉 4,347 条污染（100% 重叠面归零） | 需重扫；新增物品文本表要同步白名单 |
| 2 | `LooksLikeItemID` 对**名称侧**放宽到"任意位数纯数字"（或专门开 5 位） | +22,691 条名称，Hair/Face 缺口一次性解决 | 必须与 #1 同做，否则 `Mob` 的 6 位、`Npc` 的 3~5 位键会引入新污染；改后需重扫并核对 27,696 这个"有名称"数会**先降后升** |
| 3 | `Infos` 把"根名即 ID"的特判从 `Character.wz` 扩到任意源（现成的 `RootID`） | +461 条宠物属性 | 小；需补测试 |
| 4 | 落库顺序与到达顺序解耦（每批按 `path` 排序后再喂 `PutNames`） | 消除同 ID 多表命中时的抖动 | 小 |
| 5 | 图标索引带上**来源域**（`Npc` 域图标不参与物品行），消掉 §6 那 1,578 行串味 | 小；需扩 `icons.Index` 的值结构 |
| 6 | 若要利用 §6 的引用链（怪物分布、配方、NPC 位置） | 新检索维度 | 需新表与新抽取器，属功能扩展而非修缺 |

配套影响：`docs/09` §4 的缺口数字、§5 的污染数字、以及"有名称 27,696 / 名称+属性齐全 22,963"
这组基线在 #1+#2 之后都会变，**修完请连同本附录一起重取**。

## 8. 复现取数命令

一次性脚本（本附录所有数字都由这 8 段跑出，放在 `tmp/` 下、跑完可删；`node:sqlite` 需 `--experimental-sqlite`）：

```bash
node --experimental-sqlite tmp/wzrel3.mjs   # 20 张文本表的位数分布与 string 字段频次
node --experimental-sqlite tmp/wzrel.mjs    # 文本表→实体包覆盖率、Item/Character 内层条目、Eqp 5 位键缺口
node --experimental-sqlite tmp/wzrel2.mjs   # Map.life / Etc.NpcLocation / Etc.ItemMake / Quest 交叉引用
node --experimental-sqlite tmp/wzrel4.mjs   # Eqp 5 位键 ↔ 库内 Hair/Face 行 ↔ Character.wz 文件名
node --experimental-sqlite tmp/wzrel5.mjs   # 图标域串味 + Mob/Npc ID 与物品 ID 的重叠面
node --experimental-sqlite tmp/wzrel6.mjs   # 库内 info.mob / questId / reqJob 取值 → 实体包命中率
node --experimental-sqlite tmp/wzrel7.mjs   # Mob/Npc/Skill 三表 name 键与库内物品 ID 的重叠（§4）
node --experimental-sqlite tmp/wzrel8.mjs   # 4,733 条缺属性行按名称来源表拆开（§2 注 2）
```

纯命令行核对（不需要 node）。Git Bash 里 perl 正则**别写 `(?!…)`**，`!` 会被改写成 `\` 而报
`Sequence (?\...) not recognized`；下面几条都是本机可直接粘贴执行的：

```bash
# 每张文本表带 name 字段的条目数（对照 §1 最后一列）
for f in wz/String.wz/*.xml; do
  printf '%-16s %s\n' "$(basename $f .img.xml)" "$(grep -o '<string name="name"' $f | wc -l)"
done

# 每张表的 7/8 位数字键数 = 名称侧识别面；与上一条同表的差即"有键无名"
for f in wz/String.wz/*.xml; do
  printf '%-16s %s\n' "$(basename $f .img.xml)" "$(grep -o '<imgdir name="[0-9]\{7,8\}"' $f | wc -l)"
done

# Eqp 的 5 位键只出现在 Hair / Face 两组（输出 Face: 9359、Hair: 13366）
perl -0777 -ne 'while (/<imgdir name="(Hair|Face|Accessory|Cap|Cape|Coat|Dragon|Glove|Longcoat|Pants|PetEquip|Ring|Shield|Shoes|Weapon)">|<imgdir name="(\d{5})"/g) { if (defined $1) { $g = $1 } else { $n{$g}++ } } print "$_: $n{$_}\n" for sort keys %n' wz/String.wz/Eqp.img.xml

# 名称侧污染与宠物属性缺口
./bin/maplewzmeta.exe query -id 01110100         # 绿蘑菇        ← String/Mob 1110100
./bin/maplewzmeta.exe query -id 08220016         # 堕落的飞龙    ← Mob.wz 里没有这个怪物包
./bin/maplewzmeta.exe query -cat Pet             # 0 条          ← Item.wz/Pet 属性未抽出
./bin/maplewzmeta.exe query -id 05000000 -json   # 快动作，info={}
```
