# WZ 导出 XML 分类清单

> 统计对象：本仓库 `wz/`（62,380 个 img XML）、`wz-zh-CN/`（51 个：Etc 25 + Quest 6 + String 20）、`internal/wzxml/testdata/`（9 个），仓库内合计 62,440 个 XML。
> 统计日期：2026-09-21（与 `docs/文档索引.md` 的 62,380 口径一致：均只按 `wz/` 下 `-name '*.xml'` 计数）。
> 分类依据为三方交叉验证：目录名、8/7 位资源 ID 号段、`info/islot` 装备槽字段。
> 复现方式见文末「取数方法」。

## 1. 大类总览（按 .wz 包）

| .wz 包 | XML 数 | 承载内容 |
|---|---|---|
| Character.wz | 45,109 | 装备外观 **+ 装备属性**（唯一同时含两者的包） |
| Npc.wz | 7,506 | NPC 立绘与动作（7 位 ID） |
| Map.wz | 6,146 | 地图 5,692（Map0~Map9，9 位 ID）+ Back 131 + Obj 154 + Tile 128 + WorldMap 38 + Effect/MapHelper/Physics |
| Mob.wz | 2,389 | 怪物（7 位 ID）+ QuestCountGroup 10 |
| Item.wz | 463 | 物品定义（消耗/其他/安装/特殊/现金/宠物），详见第 4 节 |
| Reactor.wz | 472 | 反应堆（可交互场景物件） |
| Sound.wz | 51 | BGM（Bgm00~BgmTW 等）与音效（Field/Item/Weapon/Mob/UI…） |
| String.wz | 20 | **所有类别的名称与描述文本** |
| Etc.wz | 24 | 配置表（屏蔽词、任务分类、合成 ItemMake、OXQuiz、VegaSpell 等） |
| Skill.wz | 76 | 技能（按职业号段分文件，000/100~1500/…）+ MobSkill/MCSkill/BFSkill/ItemSkill |
| Morph.wz | 71 | 变身形态（4 位编号） |
| UI.wz | 20 | 界面素材（CashShop/StatusBar/Login/NameTag…） |
| Effect.wz | 17 | 特效（SetEff/SkillName1~4/Summon/Tomb…） |
| Base.wz | 3 | 物理与碰撞参数：smap、zmap、StandardPDD |
| Quest.wz | 6 | 任务数据：Act / Check / Exclusive / PQuest / QuestInfo / Say |
| TamingMob.wz | 7 | 坐骑动作资源（0001~0007） |

## 2. 装备类：全部位于 `Character.wz/<类别>/`

判类**必须以 `info/islot`（装备槽）为准**，目录名和 ID 号段都存在例外（见第 6 节）。

| 目录 | XML 数 | ID 号段（8 位前 4 位） | islot | 说明 |
|---|---|---|---|---|
| Hair | 15,252 | 0003 / 0004 / 0006 | Hr | 发型 |
| Face | 9,555 | 0002 / 0004 / 0005 | Fc | 脸型；0004 段混有 18 个 Hr |
| Weapon | 5,989 | 0121–0170 | Wp / WpSi / Si / Ri | 见第 3 节武器号段 |
| Cap | 3,208 | 0100 | Cp | 帽子 |
| Accessory | 2,888 | 0100–0119 | 11 种槽位 | 见下方饰品号段 |
| Longcoat | 2,077 | 0105 | MaPn | 长袍（上下身一体） |
| Shoes | 1,340 | 0107 | So | 鞋子 |
| Ring | 1,038 | 0111 | Ri | 戒指 |
| Cape | 1,009 | 0110 | Sr | 披风 |
| Coat | 733 | 0104 | Ma | 上衣 |
| Glove | 619 | 0108 | Gv | 手套 |
| Pants | 619 | 0106 | Pn | 裤子 |
| Shield | 143 | 0109 / 0119 | Si | 盾牌 |
| PetEquip | 308 | 0180–0183 | 无 islot | 宠物装备（圣诞鹿帽、浮云背包等） |
| TamingMob | 286 | 0190/0191/0193/0198/0199 | Tm / Sd | 0190 坐骑、0191 鞍子(Sd)、0193 机车·木马 |
| Dragon | 12 | 0194–0197 | Tm | 龙族身体 |
| Afterimage | 15 | 英文名 | — | 武器挥击残影（swordOS、crossBow、barehands…） |
| （包根目录） | 18 | 0000xxxx / 0001xxxx | Bd / Hd | 裸身、裸头基础图 |

### 饰品（Accessory 目录内 11 个号段）

| 号段 | islot | 类别（名称样本） | 文件数 |
|---|---|---|---|
| 0100 | Cp | 混入的帽子 | 2 |
| 0101 | Af | 胡须·口罩（褐色落腮胡、忍者口罩） | 579 |
| 0102 | Ay | 眼镜（时尚猫镜） | 232 |
| 0103 | Ae | 耳环（单边银色耳环） | 273 |
| 0112 | Pe | 领结·项环（黑龙项环、蝶形领结） | 350 |
| 0113 | Be | 腰带（白色腰带） | 242 |
| 0114 | Me | 勋章（冒险家勋章） | 925 |
| 0115 | Sh | — | 152 |
| 0116 | Po | — | 44 |
| 0118 | Ba | — | 68 |
| 0119 | Si | — | 21 |

## 3. 武器号段（`Character.wz/Weapon/`）

中文类别名取自 `String.wz/Eqp.img.xml` 的实证样本；`文件数` 为该号段下的 XML 个数。

| 号段 | islot | 类别 | 文件数 | 号段 | islot | 类别 | 文件数 |
|---|---|---|---|---|---|---|---|
| 0109 | Si | 混入的盾牌 | 1 | 0141 | WpSi | 双手斧 | 142 |
| 0121–0129 | Wp | 未命名/特殊条目 | 772 | 0142 | WpSi | 双手钝器·锤 | 149 |
| 0130 | Wp | 单手剑 | 282 | 0143 | Wp | 枪 | 188 |
| 0131 | Wp | 单手斧 | 161 | 0144 | Wp | 矛·戟 | 228 |
| 0132 | Wp | 单手钝器 | 207 | 0145 | 无 | 弓 | 220 |
| 0133 | Wp | 短刃 | 235 | 0146 | 无 | 弩 | 196 |
| 0134 | Si | 盾·副手 | 99 | 0147 | Wp | 拳套 | 213 |
| 0135 | Si | 盾·副手 | 342 | 0148 | Wp | 指套·拳甲 | 180 |
| 0136 | Wp | — | 123 | 0149 | Wp | 手炮 | 184 |
| 0137 | Wp | 短杖 | 180 | 0150–0155 | Wp | 双枪·双弩等 | 453 |
| 0138 | Wp | 长杖 | 215 | 0156–0159 | WpSi | — | 103 |
| 0139 | Wp | — | 6 | 0160 | Ri | 混入的戒指类 | 8 |
| 0140 | WpSi | 双手剑·大刀 | 242 | 0169 | Wp | 特殊武器 | 63 |
| | | | | 0170 | Wp | 特殊武器（护腕·坐骑武器） | 890 |

注：0121–0129、0134/0135、0150–0159 等号段在 `String.wz/Eqp.img.xml` 中没有紧邻的 `name` 节点，按名称反查类别时不要假设每个号段都有名字。

## 4. 物品类：`Item.wz/<类别>/<4 位系列号>.img.xml`

| 目录 | 文件数 | 内含条目数 | ID 段 | 含义 |
|---|---|---|---|---|
| Consume | 32 | 2,399 | 02xx | 消耗品（0200~0207 药水/宝石、卷轴、钥匙） |
| Etc | 19 | 2,694 | 04xx | 其他（040x 材料、042x/043x 任务物品） |
| Install | 2 | 863 | 03xx | 安装类物品 |
| Special | 4 | 547 | 09xx / 91xx | 特殊（0900 面具、0910/0911 状态特效） |
| Cash | 49 | 505 | 05xx | 现金商品类目（0501~0599） |
| Pet | 357 | 1 文件 = 1 宠物 | 5000 / 5001（文件名 7 位） | 宠物定义，内部子节点为 info/look 而非数字 ID |

## 5. 同一条装备的信息分在三处

1. **外观 + 属性**：`Character.wz/<类别>/<8 位 ID>.img.xml`。顶层恒为 `<imgdir name="XXXX.img">`；其 `info` 节点内联 icon/iconRaw、islot、vslot、reqJob、reqLevel、reqSTR/DEX/INT/LUK、incPAD、tuc、attackSpeed、price、cash 等；动作图在其后的 `default`/`walk1`/`attack`/`level` 等节点。
2. **名称与描述**：`String.wz/Eqp.img.xml`（16 个装备分组：Cap、Coat、Longcoat、Pants、Shoes、Glove、Shield、Cape、Ring、Accessory、PetEquip、Taming、Hair、Face、Dragon、Weapon）、`Consume`/`Etc`/`Cash`/`Ins`/`Pet`/`Mob`/`Npc`/`Map`/`Skill`/`MonsterBook`/`PetDialog` 等表。
3. **多语言**：`wz-zh-CN/{String,Etc,Quest}.wz`，共 51 个。其 `String.wz`（20 个）与 `Quest.wz`（6 个）与主目录文件清单完全一致，为同名表的另一语言版本；`Etc.wz` 有 25 个，比主目录 `Etc.wz`（24 个）多出一个 `MobLocation.img.xml`。主目录 `String.wz` 本身已是中文。

**本数据集的缺口**：没有 `Item.wz/Equip` 目录，也没有 `Base.wz/Character.img`（旧式装备属性总表）。装备属性已全部内联在 Character.wz，按官方 WZ 惯例去 Item.wz 找装备属性会一无所获。

## 6. 目录与槽位不一致的陷阱清单

分类脚本若只看目录名会出错，以下为实测到的交叉：

- Accessory 目录内混有 2 个 `islot=Cp`（帽子）文件。
- Longcoat 与 Shoes 各有 1 个 0100 号段文件（`islot=Cp`）。
- Shield 与 Weapon 都含 0119 号段；`Weapon/0134`、`Weapon/0135` 的 islot 实际是 `Si`（盾牌）。
- PetEquip（0180–0183）、TamingMob 0198 段、Weapon 0145/0146 段（弓、弩）**没有 islot 字段**，判类需回落到 ID 号段。
- Face 的 0004 号段与 Hair 重叠，其中 18 个文件 islot 为 Hr。

## 7. 取数方法（复现用）

- 规模统计已有现成工具：`go run ./cmd/wzstats -root ./wz`，输出按 .wz 包与「包/子目录」两级的文件数、节点数、uol 解析情况（**不含内容分类**）。
- 逐文件 `head -c | grep` 循环扫描 45k 文件需 40 分钟以上；改为**每个 ID 号段取 1 个代表文件**提取 islot，约 70 次读取即可覆盖全表。
- 按名称反查类别时用 `grep -oP '(?<=<imgdir name=")\d{7}(?=">)'`：`String.wz/Eqp.img.xml` 内的 ID 是 7 位且不含前导 0，写成 8 位模式会全部匹配不到。
- 导出数据集内 XML 有两种排版（Weapon 单行压缩、Cap 多行缩进），流式解析需同时兼容。
