# 10. 图标提取与 wzimgget 联动

本项目的图标（`imgdata/`）由同作者的 [sqx6781268/wzimgget](https://github.com/sqx6781268/wzimgget) 产出。
两个仓库**互不依赖**：没有代码引用、没有构建期调用，只通过一个目录 `imgdata/` 交接文件。
本文记录交接点、格式对齐细节与缺口对账口径。

## 1. 分工与链路

```
        .wz 容器（客户端）
          │                              │
   HaSuite 导出 XML                特制版导出独立 .img
          ↓                              ↓
       wz/                          Data/
          ↓                              │
  MapleWzMeta scan                wzimgget extract
          ↓                              ↓
   data/mia.db  ←────── imgdata/ ────────┘
          ↓                    ↓
      /api/items  +  /img/<id>.png   ←   serve 时按 ID 索引 PNG
```

- **数据侧**（本项目）：`wz/` 的 img XML → 解析、关联 → SQLite，产出**名称与属性**。
- **图像侧**（wzimgget）：`Data/` 的独立 `.img` → 解码 Canvas → PNG，产出**主图标**。
- 两侧在 `serve` 时才汇合：库里有行、`imgdata` 里有同名 ID 的文件，页面才显示图标列。

## 2. 为什么必须导出两次

两条管线的**输入格式不同**，且不可互换：

| 消费方 | 输入 | 由谁导出 | 本项目配置项 |
|---|---|---|---|
| MapleWzMeta `scan` | `<包>.wz/<类别>/<id>.img.xml` | HaSuite 等导出 **XML** | `locales[].wz` |
| wzimgget `extract` | `Data/<Wz>/<类别>/<id>.img`（独立 `.img`，无 .wz 头） | 客户端 `Data` 目录 | `icons.dir`（只吃它的**输出**） |

wzimgget 明确不支持 `.wz` 容器与 `list.wz`，所以它无法替本项目生成 XML；
本项目也不解析任何二进制 `.img`。**不要试图把 `Data/` 直接指给 `locales[].wz`**。

## 3. 格式对齐清单

wzimgget 的输出天然满足本项目的索引要求，逐项对照（索引实现见 `internal/icons/icons.go:42` `Build`）：

| 约定 | wzimgget 产出 | 本项目要求 | 结论 |
|---|---|---|---|
| 文件名 | `01142538.img.png` | 后缀常量 `Suffix = ".img.png"` | 直接匹配 |
| 首层目录 | `Character` / `Item` / `Npc` | 仅用于定优先级，不参与匹配 | 直接匹配 |
| 类别层 | 可选（`Npc` 平铺无类别层） | `filepath.WalkDir` 递归，层数不限 | 直接匹配 |
| ID 位数 | 保留原 IMG ID，`Npc` 常见 7 位 | `extract.NormalizeID` 左补零到 8 位 | 归一后与 `item.id` 对齐 |
| 同 ID 冲突 | 不同包可能同号 | 按 `Item` > `Character` > 其他 取优先级（`icons.go:81` `rankOf`） | 本项目侧决定 |

要点：**图标不落库**。`imgdata` 只在 `serve` / `stats` 启动时建内存索引，重跑 `scan` 与图标无关；
补导图标也**不需要**重新 `scan`，重启 `serve` 即可。

## 4. 一次跑通

```bash
# 1) 图像侧：在客户端目录（含 Data/）提取图标，输出到同级 imgdata
wzimgget.exe extract D:\game\Data -out D:\ASM\MapleWzMeta\imgdata

# 2) 数据侧：确保 wzconfig.json 的 icons.dir 指向上一步的输出目录
#    "icons": { "enabled": true, "dir": "imgdata" }     # 相对路径按配置文件所在目录解析

# 3) 元数据入库（与图标无关，可先可后）
./bin/maplewzmeta.exe scan

# 4) 起服务
./bin/maplewzmeta.exe serve
# 图标索引：28503 个文件，28307 个可用 ID（优先级0=357 优先级1=20676 优先级5=7274）
```

启动日志的「图标索引」一行就是联是否成功的判据：**数为 0 即目录指错**。

## 5. 图标缺口对账（2026-09-22 本机实测）

磁盘 `imgdata` 共 **28,504 个 PNG**，索引到 **28,503 个文件 / 28,307 个可用 ID**。
三个差值都不是缺陷：

| 差值 | 规模 | 成因 |
|---|---|---|
| 28,504 → 28,503 | 1 个 | 文件名不合规（去后缀后非纯数字），`Build` 跳过 |
| 28,503 → 28,307 | 196 个 | 跨包同 ID 被高优先级覆盖，索引计数记文件数、ID 数去重 |
| 优先级分布 | Item 357 / Character 20,676 / 其他(Npc) 7,274 | 见下 |

按顶层目录（`docs/02-目录结构说明.md` §7）：

| 目录 | 文件数 | 缺口 | 缺口性质 |
|---|---|---|---|
| `Character/` | 20,682 | 缺 `Hair`、`Face`、`Afterimage` | wzimgget 侧正常跳过：这些穿戴部件的 img 本身不含 `info/icon` 节点 |
| `Npc/` | 7,465 | 无类别层 | 布局即如此，索引不依赖类别层 |
| `Item/` | 357 | 只有 `Pet`，缺 `Etc`/`Consume`/`Cash` | 上一次导出的 `Data` 范围不全，补导即可 |

- 物品库里 `Hair=15,234`、`Face=9,555` 两个最大分类**注定没有图标**，这是格式事实而非 bug；
  它们本来也缺名称（`docs/09` §已知缺陷）。判断"图标是否漏了"之前先排除这两类。
- `Item` 侧图标覆盖率远低于物品总数，属导出范围问题，**补导后无需改任何代码**。
- 极少数 NPC（如 `9330077`）所有帧均为空画布，wzimgget 无法产出图像。

## 6. 许可证与再分发边界

- wzimgget 是 **MIT**，本项目是 **GPL-3.0**。两者无代码或链接关系，仅以文件目录交接，
  不构成 GPL 意义上的组合作品；MIT 侧也无传染性要求。
- 两边的产物（`wz/` XML、`Data/` `.img`、`imgdata/` PNG、`data/mia.db`）**都是游戏素材的衍生**，
  版权归 Nexon 及相关权利人。两个仓库都**不收录**这些内容，`.gitignore` 已排除，
  请勿提交进任一仓库，也不要随 Release 分发。

## 7. 排障

| 现象 | 先查 |
|---|---|
| 页面全是碎图 | `icons.dir` 指错目录；看启动日志「图标索引」是否为 0 |
| 索引有数但某物品无图 | 该 ID 在库里是 8 位、`imgdata` 侧是否存在对应文件；`Hair`/`Face` 本就无图标 |
| `enabled: true` 启动即报错 | 目录必须存在且是目录（宁报错也不上半屏碎图），见 `docs/07` §校验 |
| 想要无图运行 | `"icons": { "enabled": false }`，接口不返回 `icon` 字段、页面不渲染图标列，数据不受影响 |
