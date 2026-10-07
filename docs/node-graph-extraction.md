# Node-graph extraction: what is possible, and what is blocked

Status: **extraction works** (2026-10-04). Edges still to be decoded. See §6.

Goal: `.gil` → **intermediate file** → (AI) → **ASCII node-graph diagram**.

## 1. What the format gives us for free

Field **f10** of the container holds the node graphs. Measured on a real export
(`DesertStrike.gil`, f10 = 9,066 bytes), the payload is plain protobuf and contains:

| piece of information | evidence | usable? |
| --- | --- | --- |
| **graph names** | literal length-delimited strings: `关卡-建筑销毁`, `兵营出兵`, `兵营创建`, `出兵定时器`, `测试出兵定时器`, `复合节点8` | ✅ yes |
| **node positions** | 72 4-byte IEEE-754 floats in a plausible coordinate range, appearing as `tag, float32, tag, float32` pairs exactly 5 bytes apart (e.g. `@185 = -215`, `@190 = -395`) | ✅ yes |
| **custom-variable names used by the graphs** | `isBase`, `选阵营`, `选完阵营`, `spawn_unit`, `spawn_interval` | ✅ yes |
| **structure / wiring** | nested messages repeating per node, plus small sequential integers (1, 2, 3 … 12) that look like pin / node / variable **indices** | ⚠️ shape visible, exact fields need a parser |
| **node *type* names** (`创建元件`, `设置实体阵营`, `多分支` …) | **not present anywhere in the file** — the full-file string dump contains no node display names | ❌ **blocked** |

Type references are **small integers**, not name hashes:

- 210 distinct `(tag,value)` pairs in f10; **187 of them are < 400**, only 6 reach the 10000+ range
- the large values (10000, 20000, 21001, 22000, 10001 …) look like graph / node **IDs**
- so a node's type is an **index into an internal table**, and that table is not in the file

> Consequence: without a mapping table we can still draw a **structurally correct** diagram
> (boxes, positions, wiring, graph name, variable names) but the boxes would read
> `type 82` instead of `创建元件`.

## 2. The mapping problem, and the cheap way to solve it

We need `typeId → node name`. It cannot be derived from the file, so it has to be
**observed once** and then stored in `node-types.json`.

Naive approach: place one node, export, read the id, repeat — 200+ exports. Too slow.

**Grid probe (recommended):** because node **positions are stored**, one single export
can map *many* nodes at once:

1. the tool prints a numbered shopping list of node types we want (e.g. 30 common ones)
2. you create one scratch graph and place those 30 nodes on a **regular grid**
   (`x = 100, 200, 300, …`, all `y = 100`) — order matching the list
3. export, run the tool: it reports every node as `(position, typeId)`
4. because position ↔ list order is known, the tool fills in `node-types.json` automatically

One export, ~30 mappings, repeatable for the next batch. The table is version-scoped
(game updates may renumber), so it lives next to the tool and can be regenerated.

## 3. Intermediate file (proposal)

Machine-readable, one file per export — `<name>.graphs.json`:

```json
{
  "source": "DesertStrike.gil",
  "graphs": [
    {
      "id": 10000,
      "name": "关卡-建筑销毁",
      "variables": ["isBase"],
      "nodes": [
        { "id": 10001, "type": 82, "typeName": "实体销毁时", "x": -215, "y": -395 }
      ],
      "edges": [
        { "from": [10001, 0], "to": [10002, 0] }
      ]
    }
  ],
  "unknownTypes": [82, 91]
}
```

Plus a compact **AI-facing** rendering (`<name>.graphs.txt`) that is cheaper to read and
stable to diff:

```
GRAPH 关卡-建筑销毁   nodes=2
  [10001] 实体销毁时          pos=(-215,-395)
  [10002] 查询自定义变量快照   pos=(75,20)
  E [10001].0 -> [10002].0
  VAR isBase
```

The ASCII diagram is then produced **from the txt/json by the AI**, not by the tool —
that keeps the tool simple and lets the rendering style evolve freely.

## 4. ASCII rendering rules (so every diagram looks the same)

- one box per node, three lines: `┌─ id ─┐ / │ name │ / └──────┘`
- boxes laid out roughly by stored `y` (top→bottom) and `x` (left→right)
- edges `────►` with the source pin index as a label when > 1 outgoing
- variables in a footer block; unknown types rendered as `? type 82`
- graph boundaries as `╔══ GRAPH <name> ══╗`

## 5. Open questions

1. **Mapping table:** build it by grid probe (clean, self-made) — or reuse a community
   node-definition table? The latter is third-party tool *data*: node names themselves are
   public in the official docs, but this needs a compliance call before use. Default: probe.
2. **Edges:** confirm the exact field layout by parsing f10 properly (next implementation step).
3. **Other people's `.gil`:** the same code path works, but their graphs will contain node
   types we have not mapped yet → the tool should always print an `unknownTypes` list so the
   table can grow.
4. **Compound nodes (复合节点):** appear by name (`复合节点8`) — need to check whether their
   internal sub-graphs are stored inline (recursion) or referenced.

## 6. Confirmed field layout (measured, 2026-10-04)

One graph = one top-level `f1` of the node-graph field. Each is wrapped one level deep, so the
walker descends until it finds the message that carries `f2` (name) or `f3` (node entries):

```
f1  message                wrapper (one per graph)
  f1  message              graph body
    f1  message            header: f1 id, f2 container id, f3 ?, f5 0x40000005
    f2  text               graph name
    f3  message            ONE NODE ENTRY (repeated)
      f1  varint             node index (1, 2, 3, …; gaps observed, see below)
      f2  message            node record -> f1/f2/f3 ids, **f5 = node type id**
      f3  message            duplicate of the node record
      f4  message            pin / connection descriptors (references small indices)
      f5  fixed32            position X (float32)
      f6  fixed32            position Y (float32)
      f4  varint             pin count (observed values 6, 21, …)
      …nested f105.f1 text   optional custom node title
```

**Working today** (`gil-inspect -graphs`):

- graph count, graph names
- per node: index, **type id**, **position (x, y)**, custom title, and every string found in
  its subtree (variable names, branch literals like `4` / `5`)

**Measured on `DesertStrike.gil`:** 5 graphs, 33 nodes, 23 distinct type ids, e.g.

| graph | nodes |
| --- | --- |
| 关卡-建筑销毁 | 12 |
| 选完阵营 | 5 |

By context we can already name some ids by hand (type `3360` sits on the node whose subtree
mentions `isBase` → that is the custom-variable-snapshot node), which is exactly how the
lookup table will be filled.

**Still missing:** the **edges** (which pin of which node feeds which). The `f4` pin
descriptors are located and are small, repeated messages of index references — that is the
next decoding step. Until then the ASCII diagram shows **layout** (boxes at their real
relative positions) but not wiring.

## 6b. Ground truth for calibration (关卡-建筑销毁, 12 nodes)

Established from two screenshots of the real graph cross-checked against the extracted node
list (count, type multiplicity, and relative positions all agree — the second screenshot is
≈1:1 with stored editor units, offset ≈ (+453, +473) on screen).

Node ids → names (type ids now in `node-types.txt`):

| # | node | # | node |
| --- | --- | --- | --- |
| 1 | 终止定时器 | 11 | 数据类型转换 |
| 2 | 查询自定义变量快照 | 12 | 设置阵营结算成功状态 (阵营4, 失败) |
| 3 | 实体销毁时 | 13 | 设置阵营结算成功状态 (阵营5, 胜利) |
| 4 | 双分支 | 14 | 设置阵营结算成功状态 (阵营4, 胜利) |
| 6 | 是否相等 | 15 | 设置阵营结算成功状态 (阵营5, 失败) |
| 10 | 多分支 | 16 | 结算关卡 |

**The 14 real edges** (exec = white wires, data = blue wires):

| kind | edges |
| --- | --- |
| exec | 3→4, 4→1 (是), 1→10, 10→12 (case 4), 12→13, 13→16, 10→14 (case 5), 14→15, 15→16 |
| data | 3→2 (自定义变量组件快照), 2→6 (变量值), 6→4 (结果→条件), 3→4 (自定义变量组件快照→条件), 3→11 (阵营), 11→10 (输出→控制表达式) |

Unconnected: 双分支「否」and 多分支「默认」.

**Current extraction matches only 2 of these 14** (3-4 and 3-11) and invents ~16 others, all of
them hanging off nodes 2 and 3 — i.e. the reader is picking up **"where this pin's value comes
from"** references, not execution wires. This table is the calibration target: a decoder is
correct only when it reproduces exactly these 14 edges, in this direction, and nothing else.

Note that node indices are **not contiguous** (1,2,3,4,6,10,11,…): entries for 5, 7, 8, 9 are
not nodes. Whatever they are, they must be classified before indices can be trusted as identity.

## 6c. Link extraction: first attempt is NOT trustworthy yet

A first pass extracts candidate links from the pin descriptors (direct `f1`/`f2` children of
each node entry's `f4`, resolved against the graph's own node indices). It produces output,
but the result does not yet look like real wiring:

```
## 关卡-建筑销毁   (12 nodes)
links:
  1 - 2      2 - 3      2 - 4      2 - 10     2 - 12     2 - 13   …
  1 - 3      3 - 4      3 - 6      3 - 10     3 - 12     3 - 13   …
```

Two problems:

1. **Hub pattern** — nodes 2 and 3 end up connected to almost everything. Either they are
   genuine fan-out nodes, or (more likely) the reader is mixing **variable/slot references**
   into the link set.
2. **Direction unknown** — nothing in the pin bytes yet says which end is source and which is
   target, so the set is deliberately published as **undirected pairs**.

**The ASCII flow style needs both of these fixed**, because a vertical flow is defined by
wiring order, not by coordinates.

### Controlled probe that settles it in one export

Please build one scratch graph with **exactly** this shape and export it:

```
[实体创建时] ──► [设置自定义变量] ──► [发送信号]
```

- no branches, no loops, no variables other than the one 设置自定义变量 writes
- three nodes only, connected left to right in that order

With a chain this small, the pin bytes are unambiguous: whatever changes when the chain is
reversed is the source/target field, and the rest is noise we can then filter out. From that
one file we can calibrate the reader, re-run it on the real level, and only then draw flow
diagrams. A second graph containing a single 查询自定义变量节点 (nothing else) would separate
"variable reference" bytes from "wire" bytes.

**Node index gaps** (1,2,3,4,6,10,…) suggest some entries in `f3` are not nodes — possibly
group/frame records or pin-only entries. Worth classifying before the index is trusted as an
identity.

## 7. Lookup table: `node-types.txt`
Plain text, one mapping per line, `id<TAB>name` (`#` comments allowed):

```
3360	查询自定义变量快照
82	实体销毁时
```

Deliberately **not bundled with this repository**: the tool only reads a file you place
locally, so no third-party data is redistributed here. Unmapped ids are listed at the end of
every generated `.graphs.md` together with a usage count, so the table can be grown in batches
from whatever source you prefer.

## 8. Building the type table from **official tutorial levels** (planned, 2026-10-05)

The official docs ship levels as `.gil` downloads, and **each tutorial's text names the nodes it
uses** ("使用【激活基础运动器】…"). That makes them a documented ground truth:

```
official tutorial .gil  ──►  gil-inspect -scan <dir>
                               ↓
                    union of node type ids (+ counts)
                               ↓
        cross-read against the tutorial text  →  id → node name
                               ↓
                        node-types.txt grows
```

`-scan` prints the union of node type ids across every file plus the variable type ids, so a
folder of samples turns into a single work list. This is preferred over borrowing a third-party
node table: the evidence is the official text, and nothing third-party gets redistributed.

**Highest-value samples first** (node-graph-heavy tutorials): 定时器 / 全局计时器 / 关卡结算 /
阵营设置 / 过滤节点图 / 自定义变量 / 字典与结构体 / 能力单元 / 命中与受击 / 投射运动器 /
复杂造物 / 货币与商店 / 掉落物·道具·背包.

⚠️ **Official sample levels are official assets.** They may be analysed locally but must never be
committed, redistributed, or shipped with this project — `.gitignore` already excludes `*.gil`.
Only the derived `id<TAB>name` lines go into the repository.

## 9. Graph ownership: probed, and NOT derivable (2026-10-05)

Needed for questions like *"「实体销毁时」 only fires on the level entity's graphs — is this graph
attached to the level entity?"* (the official FAQ is explicit that the entity-destroy / remove
events do nothing outside the level entity's graphs).

`tools/probe_graph_ownership.py` (lives in the workspace, not in this repo) checked whether a
graph id can be followed to its owner:

```
graphs found: 5, all reporting header id 10000
→ occurrences of 10000 outside the graph field: 0 (for every graph)
```

Conclusions:

1. The header id is **not per-graph identity** — every graph reports the same value, so it is a
   scope/namespace marker rather than an owner. (In that level the graphs belong to *different*
   entities: some to the level entity, some to a building element — a shared id cannot be owner.)
2. The id is **never referenced outside the graph field**, so nothing we can read points from an
   entity/element definition to the graphs attached to it.

**Therefore ownership is not derivable from what is decoded so far.** Practical consequence:

- "this graph must be attached to the level entity" can only ever be a **reminder that lists the
  graphs containing such an event node** — which is exactly what the `destroy-event-placement`
  lint does, and it says so in the finding text;
- if real verification is ever wanted, the next place to look is how an entity's *node-graph
  config* is stored — the graph-section wrapper carries an extra nesting level, and the entity
  side may reference graphs by a hash instead of by this local id.

## 10. Asset files (`.gia`) — same container, different payload (2026-10-07)

A `.gia` (an asset from the asset centre) uses the **same container layout** as a `.gil`: a 20-byte
header of five big-endian words, then protobuf at offset 20. Observed header difference:

| word | `.gil` (level save) | `.gia` (asset) | note |
| --- | --- | --- | --- |
| 0 | size − 4 | size − 4 | same |
| 2 | **806** | **806** | constant format marker, *not* a length |
| 3 | **2** | **3** | **container kind** — level save vs asset |
| 4 | size − 24 | size − 24 | same |

The payload is a **list of asset records**, one top-level `f1` each, not a level. A record:

```
f1/f2   small id messages
f3      text     display name, e.g. "[随机]范围内取随机点（矩形）"
f5      varint
f14     message  the internal graph
    f102 { f1 <pin name>, f2 <index>, f3 {…}, f4 {…} }   pin definitions
    its node records use f5 = 0x40000001 for the compound's own boundary
    nodes, i.e. the internal shape differs from a level's
```

Measured on `常用复合节点大全v1.7` (a community pack): **176 records, 87 with a display name**
(`[执行]` 15 · `[查询]` 12 · `[变量]` 11 · `[运算]` 11 · `[矩阵]` 10 · `[随机]` 7 · `[时间]` 5 ·
`[遍历]` 4 · `[事件]` 4 · `[技能]` 1), **197 pin definitions**, and **119 distinct node type ids
over 842 occurrences**.

⭐ **Assets and levels share one node-type id space**: six ids already verified from a level export
appear in that pack (2 双分支 ×42, 14 是否相等 ×14, 180 数据类型转换 ×14, 3 多分支, 82 终止定时器,
77 结算关卡). A folder of assets is therefore a large calibration corpus.

⚠️ **Per-record attribution is unsolved**: the compound's internal node records do not use the
level's shape, so "which primitive node types does this compound use" cannot be answered yet —
which is exactly what would turn asset names into `id → name` mappings automatically.

**What does work today**: `tools/probe_gia_nodes.py --catalog OUT.md` (in the workspace) writes one
section per named record — name, pin names, and the text strings inside the record. Those strings
mix pin labels with the **author's own notes**, which is where the learning value sits, e.g.
`[变量]更新时间` → "需要在关卡实体挂载全局计时器Update" (a usage prerequisite),
`[时间]等待时间/延时` → "确保定时器名称不重复".

⚠️ Community assets are **another creator's 奇域内容**: study locally, never commit or redistribute.

### 10b. Definitions ↔ implementations pair POSITIONALLY (solved 2026-10-07)

The attribution problem above is solved. A pack's records come in two flavours, distinguished by
the record-level `f5`:

```
kind 12   definition      f1{f2:23} f2{f2:5} f3=<display name>   f14 = interface / pin defs
kind 9    implementation  f1{f2:5}                 (no name)     f13 = the graph body
```

⚠️ **The id fields are kind markers, not identities**: every definition shares `f1.f2 = 23` and
`f2.f2 = 5`; every implementation shares `f1.f2 = 5`. So there is nothing to join on — but the
pack lists **all 87 definitions first, then all 87 implementations**, and the pairing is
**positional**: definition #N ↔ implementation #N.

Inside an implementation the real nodes look like the level's:

```
f13 → f1 → f1 → f3 { f2 { f1: 10001, f2: 20000, f3: 22000, f5: 99 },  f3 { … same again … } }
                                                                     ^^ node type id
```

**Every node is stored twice** (an `f2`/`f3` duplicate pair), so raw counts must be **halved**.

**Payoff — nine ids named in one pass**, each then confirmed against the official node catalogue in
the local docs mirror (`research/official-docs/nodes.md`):

| id | official name | how the evidence reads |
| --- | --- | --- |
| 3 | 多分支 | pack's `[执行]根据实体类型执行节点` is a single 多分支 — **matches the id already verified from our own level** |
| 75 | 获取关卡实体 | 1:1 wrapper, name confirmed in the catalogue |
| 22 | **设置自定义变量** | 1:1 wrapper; and our own level's 选阵营 graph shows `设置自定义变量 "阵营"` |
| 50 | **获取自定义变量** | 1:1 wrapper |
| 310 | 获取全局计时器当前时间 | 1:1 wrapper (pack calls it "[时间]获取关卡计时器时间") |
| 10 / 11 / 12 | 加法运算 / 减法运算 / 乘法运算 | each is 3 copies of one primitive inside a "[矩阵]" wrapper |
| 226 / 227 | 逻辑与运算 / 逻辑或运算 | each is 3 copies inside "[运算]多次与运算 / 多次或运算" |

**Method rule worth keeping**: a compound whose graph is a **single node** is a 1:1 wrapper, so its
name *is* the primitive's name (verify it in the official catalogue). A compound that repeats one
primitive N times is the author's own loop — its name describes *intent*, and the id is the
primitive, **not** the compound name. Getting this wrong is how a table fills with plausible junk.

Remaining work list: the 12 ids still unmapped in our own level, plus the wider union from the pack.

### 10c. Settling an ambiguous id with a screenshot (worked 2026-10-07)

Some ids appear in no asset record at all, so the pack cannot name them — but a screenshot of the
graph they live in can, and it does not need to be pixel-perfect.

The trick: **node entries store fixed32 `x`/`y`** (fields 5 and 6 of the node entry) and those units
are **≈1:1 with editor pixels**. So a screenshot relates to the stored coordinates by a simple
translation:

```
stored ≈ image_pixel − offset        (offset derived from two already-known nodes)
```

Worked example — naming the last two ids of our `兵营出兵` graph:

1. `tools/probe_node_positions.py <file.gil> --graph 兵营出兵` printed the ten nodes with their
   coordinates. Two of them were already known, and in the image their on-screen positions differ
   by the same delta as their stored coordinates → offset ≈ (1234, 599), scale 1:1.
2. The other eight known nodes then landed within **~40 px** of prediction, which is what makes the
   model trustworthy rather than fitted.
3. The two remaining candidates, stored at `(38, −192)` and `(−334, 124)`, predicted the on-screen
   positions of 设置实体阵营 and 查询实体阵营 respectively — so those are their ids.

Two extra confirmations fell out of the same screenshot: the node feeding 创建元件's 位置/旋转 pins
reads **获取实体位置与旋转** (not the shorter 获取实体位置 this table briefly claimed), and the id that
appears **twice** in the graph is the one that appears **twice** on screen (切换造物巡逻模板).

**Rule of thumb**: repeated ids, custom titles, and the `默认/4/5` case labels of a 多分支 all have
to agree with the image before you trust a coordinate match.

## 11. 资产图已并入工具：`-graphs` 同时支持 `.gil` 与 `.gia`（2026-10-07）

`graphFrom()`（`giagraphs.go`）是统一入口：容器头**第 4 个字 = 3** 判为资产，走复合节点解码；
否则走关卡存档的 `f10` 路径。`-scan` 也一并受益（对资产会按"复合节点图"计数）。

实测（第三方复合节点包 v1.7）：**87 张图 / 473 个节点 / 47 个未映射类型**。

### 本轮新查明的事实

1. **资产图也有坐标**：在"节点分组"级 `f5/f6`（fixed32），与关卡同布局 → 能出空间布局。
2. **候选连线来自引脚描述符**：`分组 f4 → f5 子树的第一个 varint` = 被连接节点的索引。
   ⚠️ 与关卡的 `links:` 一样标为**实验性**，**尚未用截图核对**，不是执行流。
3. **容器种类在第 4 个字（偏移 12）**，不是第 3 个 —— 第 3 个字是**常量 806**。
   我一开始写错成第 3 个字，结果真机上 `isAssetContainer` 判成了关卡。
4. ⚠️ **"类型槽位"里可能装的是引用值**：例如 `1073741831 = 0x40000000 + 23`
   （与关卡里"卡牌选择器索引 1073741847"同一套基址）、`1610612771 = 0x60000000 + 99`。
   它们**不是节点类型**，工具按「引用(0x…)」渲染，并排除在"未映射工作清单"之外。
5. ⚠️ **宽容解析是必需的**：导出文件末尾有几个字节不是合法字段，
   而 `parseMessage` 要求"完整、干净地"解析 → **一个字段都拿不到**。
   新增 `parseFieldsTolerant`（遇到尾部垃圾保留已解析部分），否则资产解码结果为空。

### 顺带标定出的 6 个节点类型

| id | 官方名 | 证据 |
| --- | --- | --- |
| 248 | 获取在场玩家实体列表 | `在场玩家数量`(2 节点) 与 `遍历场上玩家`(2 节点) 的**交集** |
| 142 | 获取列表长度 | 前者的另一半（列表 → 长度），另见 8 个计数类图 |
| 509 | 列表迭代循环 | `遍历场上玩家`/`遍历范围内特定实体` 的另一半；作者注释即写"列表迭代循环" |
| 257 | 获取随机整数 | `随机执行一个节点-5位` 里直接喂给 `多分支` 的控制表达式 |
| 323 | 设置节点图变量 | `更新时间` / `节点图变量自增·自减` 共 10 处 |
| 69 | 销毁实体 | `角色碰撞死亡` 与 `销毁指定元件的所有实体 ×3` 的交集 |

**方法沉淀**：图**越小**，复合节点的名字越能定位它用的原始节点；同一 id 在多张图里出现时，
取"这些图都需要什么"的**交集**。引脚名与作者注释（记录内文本）是主要线索来源。
