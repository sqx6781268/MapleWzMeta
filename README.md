# WzItemArchive（MapleWzMeta / mia）

把冒险岛 WZ 的**导出 XML** 解析、按物品 ID 关联、落到 SQLite，然后用命令行或内置网页检索。

数据链路：

```
wz/ 导出的 img XML
  ├─ String.wz   →  8 位物品 ID → 名称 / 描述 / 分类
  └─ Item.wz、Character.wz  →  8 位物品 ID → 属性（info 扁平化为 JSON）
                    ↓  以 NormalizeID 后的 8 位零填充 ID 为主键、按 lang 分域
              data/mia.db（SQLite）
                    ↓
        CLI（query / stats）  +  HTTP 服务（内置单页检索）
```

当前本机基线：`zh-CN` 域 56,264 条物品、45,592 个已扫文件、解析失败 0，图标 28,307 个可用 ID。

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

不想装 Go 的用户可直接下载 [Releases](https://github.com/sqx6781268/MapleWzMeta/releases) 里的预编译包，覆盖 `windows-amd64`、`linux-amd64`、`linux-arm64`、`darwin-amd64`、`darwin-arm64` 五个平台。包内含可执行文件、`wzconfig.json`、`LICENSE` 与本 README；解压后仍需按第 3 节自备 `wz/` 导出数据。校验文件为 `SHA256SUMS.txt`。

> macOS 二进制未做签名与公证，首次运行需在「系统设置 → 隐私与安全性」里手动允许，或 `xattr -d com.apple.quarantine maplewzmeta`。

交叉编译（Windows 机器编 Linux 包）：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/maplewzmeta ./cmd/maplewzmeta
```

> 改了 `internal/web/static/index.html` 必须重新 `go build` 并重启服务——页面是编译期内嵌的，刷新浏览器看不到改动。

## 3. 准备数据

1. **WZ 导出目录**：默认 `wz/`，结构为 `<包>.wz/<类别>/<id>.img.xml`，例如 `wz/Item.wz/Etc/0430.img.xml`、`wz/String.wz/Eqp.img.xml`。
2. **图标（可选）**：默认 `imgdata/`，结构为 `imgdata/<Wz>/<类别>/<id>.img.png`。没有图标就把 `icons.enabled` 设为 `false`，接口不返回 `icon` 字段、页面也不渲染图标列，**不影响任何数据**。
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
      "sources": ["String.wz", "Item.wz", "Character.wz"]
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

### 查看概况

```bash
./bin/maplewzmeta.exe stats
```

先打印图标索引规模（或"未启用"），再逐语言打印物品数 / 有名称 / 有属性 / 齐全 / 文件数 / 分类 Top。

### 命令行检索

```bash
./bin/maplewzmeta.exe query -q 药水 -limit 5
./bin/maplewzmeta.exe query -prefix 0100 -cat Cap -complete
./bin/maplewzmeta.exe query -attr cash=1 -json      # 机器可读输出
./bin/maplewzmeta.exe query -unnamed                # 查导出缺口（缺名称的条目）
```

### 起服务（日常主要用法）

```bash
./bin/maplewzmeta.exe serve
# 00:20:46 图标索引：28503 个文件，28307 个可用 ID
# 00:20:46 查询服务已启动：http://127.0.0.1:8080 （语言：zh-CN，图标：开）
```

浏览器打开 <http://127.0.0.1:8080>：

- **左侧筛选栏**：分类（中文说明 + 条数，如 `武器（Weapon） 6131`，可搜索）、数据完整度、属性条件构建器、排序；
- **属性条件**可任意增删，运算符支持 `等于 / 包含 / ≥ / ≤ / 存在`，条件之间取「且」；属性键既能从下拉里选（库内真实存在的 307 个键，93 个带中文名），也能手输未收录的键；
- **结果表格**行点击 → 右上角弹出详情浮层，× / 遮罩 / Esc 关闭；
- 顶栏可切换语言域。

HTTP 接口全部只读 `GET`，可直接对接自己的脚本：

| 端点 | 用途 |
|---|---|
| `/api/items` | 列表检索，参数 `lang,q,id,cat,attr,like,min,max,has,complete,order,desc,limit,offset` |
| `/api/item?id=01000000` | 单条详情 |
| `/api/meta` | 侧边栏字典：分类与属性键的中文名、数量、是否预设 |
| `/api/stats`、`/api/categories`、`/api/langs` | 概况、分类计数、语言清单 |
| `/img/<id>.png` | 图标（仅 `icons.enabled` 时注册） |

```bash
curl -s "http://127.0.0.1:8080/api/items?lang=zh-CN&cat=Weapon&min=reqLevel=100&max=reqLevel=150"
curl -s "http://127.0.0.1:8080/api/items?lang=zh-CN&attr=cash=1&like=islot=wp&limit=3"
```

参数细则、前端行为与故障对照见 [docs/08-命令行与查询接口.md](docs/08-命令行与查询接口.md)。

## 5. 仓库结构

```
cmd/maplewzmeta/   CLI 入口（scan / query / stats / serve）
cmd/wzstats/       验证工具：全量解析但不落库，看规模与耗时
cmd/wzbind/        验证工具：名称+属性关联覆盖率
internal/wzxml/    img XML 解码（含 uol 相对路径打平、脏编码兜底）
internal/extract/  按源类型抽取元数据、ID 归一
internal/scan/     增量扫描 + 并发调度 + 批量落库
internal/store/    SQLite 表结构、upsert 合并语义、检索构造、旧库迁移
internal/config/   wzconfig.json 解析与路径基准
internal/icons/    imgdata 图标索引与 HTTP 服务
internal/web/      HTTP 接口、中文别名字典、内嵌查询页
docs/              中文说明文档（规则、实现路径、实测基线、已知缺陷）
wzconfig.json      运行配置（仓库提供）
wz/  imgdata/  data/  scripts*/  数据集、图标、客户端脚本、数据库
                   ↑ 第三方版权内容与运行时产物，不在仓库内，需自行准备
```

## 6. 文档

深入改动前先读 [docs/文档索引.md](docs/文档索引.md)：

- 架构与并发模型 → 01；数据集布局 → 02；XML 结构与坑 → 03；
- 物品 ID 归一与关联 → 04；装备槽 `islot` 与号段 → 05；存储与检索构造 → 06；
- 配置字段 → 07；CLI/HTTP/页面 → 08；**测试基线、实测数字与已知缺陷** → 09。
- 附录：**WZ 导出 XML 分类清单**（可复现的计数底稿）、
  **实体与 String.wz 的关联**（20 张文本表各自属于哪个实体域、ID 位数形态、实测关联率与跨实体引用链）。

已知缺陷（例如名称侧跨实体表污染、Hair/Face 缺名称的成因）都记录在 09，改管线前请对照，别把预期内的数字下降当成回归。

## 7. 常见问题

| 现象 | 处置 |
|---|---|
| `bind: Only one usage of each socket address` | 8080 上有旧实例。`netstat -ano \| grep :8080` 取 PID 停掉，或 `serve -addr 127.0.0.1:8081` 换端口 |
| 改了页面没反应 | 前端是 `go:embed`，必须重新 `go build` 并重启 `serve` |
| 页面全是碎图 | `icons.dir` 指错目录；启动日志的图标索引数为 0 即目录不对 |
| 某语言条目为 0 | 该 locale 的 `sources` 没覆盖到含物品 ID 的包，或 `wz` 目录指错 |
| 条件叠加返回 0 | 先分别确认两个集合都非空再看交集。例如 `cash=1` 的物品 `reqLevel` 恒为 0，与 `reqLevel≥120` 组合必然 0 命中，这是数据事实 |
| 相对路径找不到文件 | 配置里的相对路径按**配置文件所在目录**解析；`-db` 覆盖项按**进程工作目录**解析，两者基准不同 |

## 8. 许可证

本项目采用 **GNU General Public License v3.0**（见 [LICENSE](LICENSE)）。

- 代码与文档：Copyright © 2026 sqx6781268，依 GPL-3.0 分发——使用、修改或分发本代码（含衍生作品）时，必须以同样的许可证公开完整源码。
- 游戏数据：本仓库**不包含**任何冒险岛客户端素材。`wz/`、`imgdata/`、`scripts*/` 等目录下的数据、图像与脚本版权归 Nexon 及相关权利人所有，本项目不对其主张任何权利，也不授予再分发许可。请勿将客户端导出内容提交进本仓库或随构建产物分发。
- 本项目为非官方社区工具，与 Nexon 无关。
