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
