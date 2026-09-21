# 03 · img XML 结构解析

## 1. 一个 img XML 文件长什么样

WZ 的每个 `.img` 导出为一个 XML，**根节点恒为 `<imgdir>`**，其 `name` 是原 img 名（如 `0430.img`、`Eqp.img`）。
值全部承载在**属性**上，文本节点一律无意义（解析器直接忽略 `CharData`，`internal/wzxml/node.go:134`）。

```xml
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<imgdir name="0400.img" indent="2" media="NONE">
  <imgdir name="04000000">
    <imgdir name="info">
      <canvas name="icon" width="29" height="30">
        <vector name="origin" x="-2" y="30"/>
      </canvas>
      <int name="price" value="3"/>
      <string name="islot" value="Cp"/>
      <uol name="iconRaw" value="../icon/icon"/>
    </imgdir>
  </imgdir>
</imgdir>
```

### 两种排版（同一份数据，导出器不同）

| 排版 | 特征 | 例子 |
|---|---|---|
| 多行缩进 | 带 `indent="2" media="NONE"`，`canvas` 用 `width`/`height` | `Item.wz/Etc/*.img.xml`、`Character.wz/Cap/*` |
| 单行压缩 | 整个文件一行，无 `indent`/`media` | `String.wz/Cash.img.xml`、部分 `Character.wz/Weapon` |

**解析器必须同时吃下两种**，且不能依赖缩进或换行做语义判断（`repairTruncatedTags` 是唯一按行处理的地方，
只用于补 `/>`）。

## 2. 节点类型清单（实测）

在 `Item.wz` + `String.wz` + 200 个 `Character.wz` 文件的抽样中，类型分布为：

| 类型 | 抽样出现次数 | 语义 | 属性形态 |
|---|---|---|---|
| `string` | 290,137 | 文本值 | `value` |
| `imgdir` | 160,169 | 容器/分组 | 只有 `name`（+ 偶发扩展属性） |
| `int` | 147,625 | 整数 | `value`（部分导出器另给 `hexvalue`） |
| `vector` | 112,728 | 二维坐标 | `x` / `y`，**无 value** |
| `canvas` | 54,947 | 位图占位 | `width`/`height` **或** `format`/`scale`；可含 `vector origin`、`imgdir map` 子节点 |
| `uol` | 20,715 | 引用复用 | `value` 为相对路径，见 §5 |
| `double` / `float` | 32 / 11 | 浮点 | `value` |
| `short` | 少量 | 小整数 | `value`（如 `<short name="face" value="1"/>`） |
| `null` | 少量 | 空标记 | **只有 `name`，无 `value`**（如 `<null name="[可恢复全部的HP/MP…]"/>`） |
| `bool` / `long` / `aqua` | 本数据集未出现 | WZ 惯例类型 | 解析器不裁剪类型，出现即原样保留 |

> **不做类型白名单**：`Node.Type` 就是 XML 标签名的原样字符串（`internal/wzxml/node.go:19`）。
> 遇到没见过的新类型无需改代码，只有需要**解释**它时才动 `extract`。

## 3. 属性形态不一致（必须原样保留）

除 `name` / `value` 之外，所有属性进 `Node.Attrs`（`internal/wzxml/node.go:106`），**不做白名单裁剪**。原因：

- `canvas` 可能是 `width="29" height="30"`，也可能是 `format="2" scale="0"`；
- 根 `imgdir` 可能带 `indent`、`media`；
- 部分导出器给 `<int>` 额外写 `hexvalue="0x1F4"`。

抽样中属性出现频次：`name` 335,082｜`value` 170,314｜`x`/`y` 各 70,521｜`width`/`height` 各 33,275｜
`format`/`scale` 各 4,653｜`indent`/`media` 各 2。

`Node.IntValue`（`internal/wzxml/node.go:224`）因此实现为：**先按十进制读 `value`，失败再退到 `hexvalue` 按 16 进制读**。
本数据集实测未出现 `hexvalue`，该分支是防御性的。

## 4. 文件级脏数据与容错

| 脏数据 | 现象 | 处理 | 位置 |
|---|---|---|---|
| UTF-8 BOM | 开头 `EF BB BF` | `Peek(3)` 后丢弃 | `internal/wzxml/node.go:71`；`DecodeFileRobust` 里再 `TrimPrefix` 一次 |
| 未定义实体 / 控制字符 | 严格模式直接报错 | `xml.Decoder.Strict = false` | `internal/wzxml/node.go:39` |
| 标签被截断 | `  <sound name="Subway"` 缺 `value` 与 `/>` | 仅对"以引号收尾且不以 `>` 收尾、且不是 `</`/`<?`"的行补 `/>`，**不做通用 HTML 纠错** | `internal/wzxml/repair.go:73` |
| 声明了非 UTF-8 | `encoding="gb18030"` 等 | 内容确认为纯 ASCII → 把声明改写成 `UTF-8` 并记 `NormalizedEncoding`；含多字节 → 返回 `ErrNonUTF8Content`，**绝不按 UTF-8 猜** | `internal/wzxml/repair.go:47` |
| 非法 UTF-8 字节 | 归档到 JSON 会变 U+FFFD | 自研 `appendJSONQuote` 按字节写出，控制字符转 `\u00XX` | `internal/wzxml/uol.go:256` |

解码入口的选择：

- `DecodeFile` —— 严格一档，测试与干净文件用；
- `DecodeFileRobust` —— 管线实际用的，先正常解、失败再修标签重试，返回 `RepairInfo{RepairedTags, DeclaredEncoding, NormalizedEncoding}`。
  **修复只发生在内存副本，源文件保持原样。**

根约束：`Decode` 只接受单根；根之后还有元素或非空文本 → `ErrMultiRoot`；完全没有元素 → `ErrEmpty`
（`internal/wzxml/node.go:83`）。

## 5. `uol` 引用语义与打平

### 语义

`<uol name="X" value="a/b/c"/>` 的 `value` 是**相对于该 uol 所在父 imgdir** 的路径，段间以 `/` 分隔，支持 `..` 与 `.`。
例：`blink/0` 内的 `"../../default/default"` 指向**根 imgdir** 下的 `default/default`。

实测抽样（200 个 Character 文件）含 20,715 个 uol —— 不打平则属性抽取会大面积缺字段。

### 打平算法（`internal/wzxml/uol.go:28`）

```
建 parent 索引 → 最多 16 趟（MaxUolPass）：
    收集所有仍是 uol 的节点
    对每个：① 父相对解析  ② 失败则根相对兜底  ③ 目标为 nil / 自身 → 跳过
    命中则先 Clone 出目标快照，再改写原节点（Type/Value/Attrs/Children 全部替换，Name 保留）
    本趟 progress == 0 → 提前 break（说明全是死引用）
收尾：Unresolved = 仍为 uol 的节点数
```

三条不显然但关键的规则：

1. **父相对优先**，只有父相对解析失败才尝试根相对（`lookupFromRoot`，`internal/wzxml/uol.go:77`）。
   原因：实测存在 `default/default` 这种"按文件根书写却忘了 `../`"的引用值。根相对时 `..` 按"到顶即止"处理。
2. **先 `Clone` 再改写**：目标可能正是自身的祖先，直接展开会读到半改写状态。
3. **16 趟上限 + 无进展即停**：自引用链（`a → b → a`）不会无限展开。

未解析的 uol **不报错**：节点保持原样，由上层决定。`Node.ListUnresolved(limit)` 给出
`{path, value}` 清单，`wzstats` 用它打印样例（最多 30 条）。
常见成因是**跨 `.img` 引用**（导出成单文件后目标根本不在树里），属数据侧限制。

## 6. 节点树接口（`internal/wzxml/node.go`）

| 方法 | 用途 | 注意 |
|---|---|---|
| `Child(name)` / `ChildAt(i)` | 取子节点 | 找不到返回 `nil`，且**所有方法对 nil 接收者安全**，可链式裸用 |
| `Find(path...)` | 逐层下钻 | 任一层缺失即 `nil` |
| `StrValue(name)` / `IntValue(name)` | 取子节点值 | 整数值走 §3 的十进制→hex 退路 |
| `ChildNames()` | 按文档顺序取全部子节点名 | `extract` 用它记 `Parts` |
| `Walk(fn)` | 前序遍历，`fn(parent, node)`，返回 `false` 中止 | 根节点 `parent == nil` |
| `Count()` | 节点总数 / 最大深度 / 按类型计数 | `wzstats` 的数据源 |
| `Clone()` | 深拷贝（含 `Origin`） | uol 展开用 |
| `WriteXML(w)` | 紧凑单行输出，属性序为 `name`、`value`、其余字典序 | **不追求与任意导出器字节级一致**，仅用于对照校验 |
| `JSON()` / `AppendJSON(dst)` | 确定性 JSON，键 `_t/_n/_v/_o/_a/_c` | 键序固定，便于 golden 与统计对比 |

`Origin` 字段只在 uol 展开后有值，记录原始 `value`，用于回溯"这个子树是引用来的"。

## 7. 加新类型/新属性时的落点

1. 只是"看到了新东西" → 不用改，`Attrs` 与 `Type` 已原样保留。
2. 需要**检索**它 → 改 `flattenScalars`（`internal/extract/extract.go:143`）的扁平化规则，注意 §8 的键名冲突。
3. 新属性影响**整数解释** → 只改 `IntValue`。
4. 新的脏数据形态 → 加到 `DecodeFileRobust`，**必须**同步在 `RepairInfo` 里计数，并补 `repair_test.go` 用例。

## 8. 扁平化键名规则与冲突

`flattenScalars` 把 `info` 子树压成 `map[string]string`：

| 输入 | 输出键 |
|---|---|
| `<int name="price" value="3"/>` | `price = 3` |
| `<canvas name="icon">` + `<vector name="origin" x= y=/>` | `icon.width`、`icon.origin.x`、`icon.origin.y` |
| `info` 节点自身的扩展属性 | `@indent = 2` 之类，加 `@` 前缀 |
| 嵌套 `imgdir`（如 `state`、`skill`） | **跳过**，只留在 `Parts` 里记名字 |

由此产生的两个约束（`internal/store/store.go:674` `jsonPath` 必须处理）：

- 键里**可以含点**（`icon.width`），因此 JSON 路径必须写成带引号的形式 `$."icon.width"`，否则点被当成嵌套；
- 键里**可以含 `@`**（`@indent`），同样必须引号包裹。
