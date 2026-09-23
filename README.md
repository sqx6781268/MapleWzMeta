# WzItemArchive（MapleWzMeta / mia）

把冒险岛 WZ 的**导出 XML** 解析、按实体 ID 关联、落到 SQLite，然后用命令行或内置网页检索。
覆盖**物品 / NPC / 怪物 / 技能 / 变身 / 反应堆六个实体域**，六域共用同一条解析管线、各入一张同构表。

数据链路：

```
wz/ 导出的 img XML
  ├─ String.wz        →  8 位实体 ID → 名称 / 描述 / 分类（表名白名单定域：物品·NPC·怪物·技能）
  ├─ Item.wz、Character.wz  →  物品属性（info 扁平化为 JSON）
  ├─ Mob.wz、Npc.wz         →  怪物 / NPC 属性
  └─ Skill.wz、Morph.wz、Reactor.wz  →  技能 / 变身 / 反应堆属性
                    ↓  以 NormalizeID 后的 8 位零填充 ID 为主键、按 (lang, 实体域) 分域
        data/mia.db（SQLite：item / npc / mob / skill / morph / reactor 六张同构表 + wz_file / scan_run）
                    ↓
        CLI（query / stats）  +  HTTP 服务（内置单页检索）
```

图标另走一路，**不落库、且按实体域取用**：`imgdata/`（由 [wzimgget](https://github.com/sqx6781268/wzimgget) 从客户端 `Data` 的独立 `.img` 提取）→ `serve` 启动时按 `(域, 8 位零填充 ID)` 建内存索引 → `/img/<id>.png?for=<kind>`（物品域只认 `{Item, Character}` 目录，不会拿到 NPC 立绘）。

当前本机基线：`zh-CN` 物品 52,558 / NPC 7,474 / 怪物 2,532 / 技能 544 / 变身 71 / 反应堆 462，图标 36,646 个可用 ID。分域细则见 [docs/11-NPC与怪物解析.md](docs/11-NPC与怪物解析.md) 与 [docs/09](docs/09-验证基线与已知缺陷.md) §2。

> **仓库内容范围**：本仓库只开源**代码与文档**。`wz/`、`imgdata/`、`scripts*/` 等目录是冒险岛客户端的导出内容与素材，版权归 Nexon 及其权利人所有，**不随本仓库分发**，也不在 `.gitignore` 的收录范围内。使用者需自行准备导出数据，详见第 3 节。

---

## 1. 环境要求

| 项 | 要求 | 说明 |
|---|---|---|
| Go | 1.27 及以上（`go.mod` 声明 `go 1.27.0`） | 唯一构建依赖 |
| C 编译器 | **不需要** | SQLite 驱动用纯 Go 的 `modernc.org/sqlite`，本机没有 gcc 也能编译 |
| 前端工具链 | **不需要** | 查询页是 `go:embed` 进去的单文件 `internal/web/static/index.html`，无 npm / 无构建 |
| 第三方框架 | 无 | HTTP 用标准库 `net/http` + `flag`，刻意不引 gin/echo |

## 2. 编译

```bash
git clone https://github.com/sqx6781268/MapleWzMeta.git
cd MapleWzMeta

# 当前平台
go build -o bin/maplewzmeta.exe ./cmd/maplewzmeta

# 只验证能不能编（含 vet 与全量测试）
go build ./... && go vet ./... && go test ./... -count=1
```

产物 `bin/maplewzmeta.exe` 是**单文件**，内含查询页；把它连同 `wzconfig.json`、`wz/`、`imgdata/`、`data/mia.db` 一起拷走即可运行。其中 `data/mia.db` 由 `scan` 生成，仓库不提供。

已安装 Go 的用户也可直接拉取模块编译：

```bash
go install github.com/sqx6781268/MapleWzMeta/cmd/maplewzmeta@latest
```

不想装 Go 的用户可直接下载 [Releases](https://github.com/sqx6781268/MapleWzMeta/releases) 里的预编译包，覆盖 `windows-amd64`、`linux-amd64`、`linux-arm64`、`darwin-amd64`、`darwin-arm64` 五个平台。包内含可执行文件、`wzconfig.json`、`CHANGELOG.md`、`LICENSE` 与本 README；解压后仍需按第 3 节自备 `wz/` 导出数据。校验文件为 `SHA256SUMS.txt`。

> macOS 二进制未做签名与公证，首次运行需在「系统设置 → 隐私与安全性」里手动允许，或 `xattr -d com.apple.quarantine maplewzmeta`。

交叉编译（Windows 机器编 Linux 包）：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/maplewzmeta ./cmd/maplewzmeta
```

> 改了 `internal/web/static/index.html` 必须重新 `go build` 并重启服务——页面是编译期内嵌的，刷新浏览器看不到改动。

## 3. 准备数据

1. **WZ 导出目录**：默认 `wz/`，结构为 `<包>.wz/<类别>/<id>.img.xml`，例如 `wz/Item.wz/Etc/0430.img.xml`、`wz/String.wz/Eqp.img.xml`。
2. **图标（可选）**：默认 `imgdata/`，结构为 `imgdata/<Wz>/<类别>/<id>.img.png`。图标由同作者的 [wzimgget](https://github.com/sqx6781268/wzimgget) 从客户端 `Data` 目录的独立 `.img` 提取产出，两仓库只以该目录交接文件，联动步骤与缺口对账见 [docs/10-图标提取与wzimgget联动.md](docs/10-图标提取与wzimgget联动.md)。没有图标就把 `icons.enabled` 设为 `false`，接口不返回 `icon` 字段、页面也不渲染图标列，**不影响任何数据**。
3. **配置**：根目录 `wzconfig.json`，支持整行 `//` 注释；相对路径一律按**配置文件所在目录**解析，与启动目录无关。

```jsonc
{
  "db": "data/mia.db",          // SQLite 路径
  "addr": "127.0.0.1:8080",     // serve 监听地址
  "workers": 0,                 // 并发解析协程数，0 = 按 CPU 自动
  "icons": { "enabled": true, "dir": "imgdata" },
  "locales": [                  // 一个条目 = 一套语言，入库后按 lang 分域互不覆盖
    {
      "lang": "zh-CN",
      "label": "简体中文",
      "wz": "wz",
      "sources": ["String.wz", "Item.wz", "Character.wz", "Mob.wz", "Npc.wz"]
    }
  ]
}
```

加一门语言只需在 `locales` 追加一项，然后重跑 `scan`，无需改代码。字段细则见 [docs/07-配置文件说明.md](docs/07-配置文件说明.md)。

## 4. 使用

四个子命令共用同一份配置，命令行只放**筛选**和**临时覆盖**参数。

### 扫描入库

```bash
./bin/maplewzmeta.exe scan                 # 配置里全部语言
./bin/maplewzmeta.exe scan -lang zh-CN     # 只扫一个语言
./bin/maplewzmeta.exe scan -limit 50       # 小批量试跑（每个语言最多 50 个待处理文件）
```

增量判据是文件的 `size + mtime`：首次全量约 38s，**重跑同一份导出应输出「跳过未变 = 磁盘文件数、解析 0、用时 0s」**。

```bash
./bin/maplewzmeta.exe scan -force              # 忽略指纹全部重解（补齐文件级溯源）
./bin/maplewzmeta.exe scan -overwrite          # 以文件为准覆盖：info 整包替换 + 清掉失效贡献
```

两个开关互相独立：`-force` 管"要不要重解"，`-overwrite` 管"落库时覆盖还是累积"。
命令行**始终保护**管理页手工改过的行（`item.edited=1`）。

### 查看概况

```bash
./bin/maplewzmeta.exe stats
```

先打印图标索引规模（或"未启用"），再逐语言打印规模：每个语言下**分六个实体域各一行**，各行的条数 / 有名称 / 有属性 / 齐全占比，末尾的文件数是语言域级别的合计。

### 命令行检索

```bash
./bin/maplewzmeta.exe query -q 药水 -limit 5
./bin/maplewzmeta.exe query -prefix 0100 -cat Cap -complete
./bin/maplewzmeta.exe query -attr cash=1 -json      # 机器可读输出
./bin/maplewzmeta.exe query -unnamed                # 查导出缺口（缺名称的条目）
./bin/maplewzmeta.exe query -kind mob -q 僵尸蘑菇   # 检索怪物域（-kind item|npc|mob，缺省 item）
```

### 起服务（日常主要用法）

```bash
./bin/maplewzmeta.exe serve
# 00:20:46 图标索引：28503 个文件，28307 个可用 ID（按域：item=…，npc=…，mob=…）
# 00:20:46 查询服务已启动：http://127.0.0.1:8080 （语言：zh-CN，图标：开）
```

浏览器打开 <http://127.0.0.1:8080>：

- **顶栏「实体」下拉**切换物品 / NPC / 怪物 / 技能 / 变身 / 反应堆六域，切域会重拉 meta、统计与检索；非物品域下左侧分类块自动隐藏（这些域一文件一实体、没有分类目录）；
- **顶栏右上角「夜晚 / 白天」按钮**切换暗色配色（配色只改 CSS 变量，选择记在 `localStorage`，检索页与管理页共用同一个键、互相同步；首屏在 `<head>` 里提前套用，不会闪白）；
- **左侧筛选栏**：分类（中文说明 + 条数，如 `武器（Weapon） 6131`，可搜索）、数据完整度、属性条件构建器、排序；
- **属性条件**可任意增删，运算符支持 `等于 / 包含 / ≥ / ≤ / 存在`，条件之间取「且」；属性键既能从下拉里选（按当前域列出的真实键，带中文名的做成常用按钮），也能手输未收录的键；
- **结果表格**行点击 → 右上角弹出详情浮层，× / 遮罩 / Esc 关闭；浮层底部标明这条数据的**名称来自哪个 `String.wz` 表、属性来自哪个文件**，改完 XML 就知道该重载谁；
- 表格区是一张固定高度的卡片：**内部滚动、表头吸顶、分页条钉在卡片底部**（翻页不用把页面滚到底），分页条最左是页码 `1 … 当前页前后各 5 页 … 末页`；关键属性摘要限宽可换行、**最多 3 行**，超出截断为「…另 N 项」；
- 顶栏可切换语言域；**顶栏右侧「管理页」入口**直达 `/admin`（`admin.enabled=false` 时自动隐藏）。

HTTP 接口全部只读 `GET`，检索端点都带 `kind`（缺省即物品域），可直接对接自己的脚本：

| 端点 | 用途 |
|---|---|
| `/api/items` | 列表检索，参数 `kind,lang,q,id,cat,attr,like,min,max,has,complete,order,desc,limit,offset` |
| `/api/item?id=01000000` | 单条详情 |
| `/api/meta` | 侧边栏字典：分类与属性键的中文名、数量、是否预设（属性说明按域分表） |
| `/api/stats`、`/api/categories`、`/api/langs` | 概况、分类计数、语言清单 |
| `/img/<id>.png?for=<kind>` | 图标（仅 `icons.enabled` 时注册；`for` 缺省即物品域） |

```bash
curl -s "http://127.0.0.1:8080/api/items?lang=zh-CN&cat=Weapon&min=reqLevel=100&max=reqLevel=150"
curl -s "http://127.0.0.1:8080/api/items?lang=zh-CN&attr=cash=1&like=islot=wp&limit=3"
```

参数细则、前端行为与故障对照见 [docs/08-命令行与查询接口.md](docs/08-命令行与查询接口.md)。

### 管理页（XML 变了以后同步数据库）

浏览器打开 <http://127.0.0.1:8080/admin>（或点首页右上角「管理页」），四个标签页：

| 标签页 | 做什么 |
|---|---|
| **比对与重载** | 只读比对磁盘指纹与库内指纹，列出 `新增 / 变更 / 已删除`；勾选文件 → 重新解析并**以文件为准覆盖**（`info` 整包替换、XML 里删掉的字段和条目在库里同步消失）；也可按状态批量重载或全库强制重扫。手工行统计显示物品/NPC/怪物**分域明细** |
| **文件记录** | 分页看 `wz_file`（大小、修改时间、贡献条目数、**文件级溯源 ID 数**），单行强制重载 |
| **实体修正** | 顶栏切换实体域，检索并手工改名称/描述/分类/属性 JSON，可删除；改过的行打「手工」标记，**默认不受重载覆盖**（保护按域生效） |
| **扫描历史** | 每次扫描/重载的耗时与成败 |

安全闸门：`wzconfig.json` 的 `admin.token` 非空时所有写接口查 `X-Admin-Token` 头；
留空则**只放行本机回环地址**。把 `addr` 绑到局域网前务必先设口令。`admin.enabled=false` 可整体关闭。

> 旧库第一次用要先跑一次「全库强制重扫」：`wz_file.ids`（文件级溯源）补齐之后，
> 逐文件重载才能清掉该文件不再贡献的历史数据。

## 5. 仓库结构

```
cmd/maplewzmeta/   CLI 入口（scan / query / stats / serve）
cmd/wzstats/       验证工具：全量解析但不落库，看规模与耗时
cmd/wzbind/        验证工具：名称+属性关联覆盖率
internal/wzxml/    img XML 解码（含 uol 相对路径打平、脏编码兜底）
internal/extract/  按源类型抽取元数据、ID 归一、按表名白名单/包名判定实体域（物品·NPC·怪物）
internal/scan/     增量扫描 + 并发调度 + 定序落库（落库前按 (ID,来源) 排序）
internal/store/    SQLite 三张同构实体表 + 文件指纹、upsert 合并语义、kind 前缀溯源、检索构造、旧库迁移
internal/config/   wzconfig.json 解析与路径基准
internal/icons/    imgdata 图标索引与 HTTP 服务（按实体域取用）
internal/web/      HTTP 接口（带 kind）、分域中文别名字典、内嵌查询页
docs/              中文说明文档（规则、实现路径、实测基线、已知缺陷）
wzconfig.json      运行配置（仓库提供）
wz/  imgdata/  data/  scripts*/  数据集、图标、客户端脚本、数据库
                   ↑ 第三方版权内容与运行时产物，不在仓库内，需自行准备
```

## 6. 文档

深入改动前先读 [docs/文档索引.md](docs/文档索引.md)：

- 架构与并发模型 → 01；数据集布局 → 02；XML 结构与坑 → 03；
- 物品 ID 归一与关联 → 04；装备槽 `islot` 与号段 → 05；存储与检索构造 → 06；
- 配置字段 → 07；CLI/HTTP/页面 → 08；**测试基线、实测数字与已知缺陷** → 09；
  **图标提取联动（wzimgget）** → 10（交接格式、一次跑通步骤、图标缺口对账）；
  **NPC 与怪物解析** → 11（这两个域的命名/属性来源、ID 位数与归一、跨域重叠与分表结论、常用属性键）。
- 附录：**WZ 导出 XML 分类清单**（可复现的计数底稿）、
  **实体与 String.wz 的关联**（20 张文本表各自属于哪个实体域、ID 位数形态、实测关联率与跨实体引用链）。

已知缺陷（例如名称侧跨实体表污染、Hair/Face 缺名称的成因）都记录在 09，改管线前请对照，别把预期内的数字下降当成回归。

## 7. 常见问题

| 现象 | 处置 |
|---|---|
| `bind: Only one usage of each socket address` | 8080 上有旧实例。`netstat -ano \| grep :8080` 取 PID 停掉，或 `serve -addr 127.0.0.1:8081` 换端口 |
| 改了页面没反应 | 前端是 `go:embed`，必须重新 `go build` 并重启 `serve` |
| 页面全是碎图 | `icons.dir` 指错目录；启动日志的图标索引数为 0 即目录不对 |
| 想用 zip 分发图标 | `icons.dir` 直接写 `imgdata.zip`（Store 打包最快），重启 `serve`；索引只在启动时建，改包不会自动生效 |
| 某语言条目为 0 | 该 locale 的 `sources` 没覆盖到含物品 ID 的包，或 `wz` 目录指错 |
| 条件叠加返回 0 | 先分别确认两个集合都非空再看交集。例如 `cash=1` 的物品 `reqLevel` 恒为 0，与 `reqLevel≥120` 组合必然 0 命中，这是数据事实 |
| 相对路径找不到文件 | 配置里的相对路径按**配置文件所在目录**解析；`-db` 覆盖项按**进程工作目录**解析，两者基准不同 |
| 管理页报 401 / 403 | 401 是 `admin.token` 非空但页面口令没填对（顶部重填）；403 是口令留空却从非回环地址访问，设口令或用 `127.0.0.1` 打开 |
| 重载后库里旧字段还在 | 该语言域还没补齐文件级溯源，先跑一次「全库强制重扫」；或该行有手工标记被保护了，勾「同时覆盖手工修改过的行」 |

## 8. 许可证

本项目采用 **GNU General Public License v3.0**（见 [LICENSE](LICENSE)）。

- 代码与文档：Copyright © 2026 sqx6781268，依 GPL-3.0 分发——使用、修改或分发本代码（含衍生作品）时，必须以同样的许可证公开完整源码。
- 游戏数据：本仓库**不包含**任何冒险岛客户端素材。`wz/`、`imgdata/`、`scripts*/` 等目录下的数据、图像与脚本版权归 Nexon 及相关权利人所有，本项目不对其主张任何权利，也不授予再分发许可。请勿将客户端导出内容提交进本仓库或随构建产物分发。
- 本项目为非官方社区工具，与 Nexon 无关。
