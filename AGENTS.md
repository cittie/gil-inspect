# gil-inspect — agent guide

**If you are an AI assistant that was handed this repository URL, read this file first.** It is
the shortest path from "here is a link" to "I can build the tool, run the right command, and
interpret the output without over-claiming".

Human-facing docs: [`README.md`](README.md) (English) · [`README.zh-CN.md`](README.zh-CN.md) (中文).

---

## 1. What this is

A **read-only** inspector for `.gil` files — the level exports produced by the official
*export save* feature of the Genshin Impact UGC editor (Miliastra Wonderland / 千星沙箱).

Typical use: you built a level, exported it, and want to know **what is actually inside the file**
— which elements, custom variables and node graphs exist, what changed since the last export, and
whether anything looks inconsistent — **without opening the editor**.

## 2. Build (no dependencies, Go 1.27+)

```
go build ./...          # or: go build -o gil-inspect ./...
go test ./...           # 20 tests
go vet ./...
```

## 3. The four commands (this is the whole interface)

| command | writes | use it when |
| --- | --- | --- |
| `gil-inspect -path X.gil` | `X.inspect.md`, `gil-snapshot.json` | inventory: container header, top-level field map, keyword hits, **diff vs the previous export** |
| `gil-inspect -path X.gil -graphs` | `X.graphs.md` | you want the node graphs as **plain markdown** (graph names, node list, type ids/names, custom titles, referenced variable names) |
| `gil-inspect -path X.gil -lint` | `X.lint.md` | routine checks: dangling references, naming, graph size, **custom-variable types**, declared-variable inventory |
| `gil-inspect -scan DIR` | stdout | a **folder** of exports: per-file summary + the **union of node/variable type ids** (calibration work list) |

Debug aids, only needed when decoding something new:
`-graphdump` (indented protobuf tree of the graph field) · `-pins` (raw pin descriptors).

Other flags: `-nodetypes FILE` (default `node-types.txt`) · `-graphfield N` (default 10) ·
`-keys a,b,c` · `-keyfile FILE` · `-out FILE` · `-snapshot FILE`.

## 4. How to interpret the output

**`X.inspect.md`**
- `f<N>` rows are top-level container fields. Observed meaning: `f4` element/template library,
  `f5` entity placement, `f8` element definitions, **`f10` node graphs**, `f15` inventory.
- The "size changes vs snapshot" section is how you answer *"what did this export change?"* —
  compare field sizes against the previous `gil-snapshot.json`.
- The field walk may stop a few bytes before EOF; that is **normal**, not corruption.

**`X.graphs.md`**
- `nodes:` lines are `index<TAB>name<TAB>"custom title"<TAB>refs: …`.
- `?N` means the node type id is not mapped yet → add `N<TAB>name` to `node-types.txt`.
- **`links:` is EXPERIMENTAL.** It does not reproduce real wiring (measured: 2 of 14 known edges).
  Do **not** draw a flow diagram from it and present it as the graph's logic.

**`X.lint.md`**
- Findings are **things to verify, not verdicts** — the container lacks the context to be certain.
- `error` (❌) = a real inconsistency (e.g. one variable name declared with two different types).
- `warn` (⚠️) = likely mistake (e.g. a variable named `*_unit` declared as 整数).
- `info` (ℹ️) = inventory / not-yet-mapped ids.
- The report ends with a **declared-variable table** (name / type / declaration sites / default).
- The report states its boundary against the editor's own checks; repeated here because it matters:
  the platform has **试玩校验** (blocking) and **风险检查** (non-blocking, data-level). **Whatever
  they report is authoritative.** Verified blind spot we do cover: **a custom variable declared
  with the wrong type is NOT reported there** — the mechanism just silently does nothing.

## 5. Limits — do not promise these

| not available | why |
| --- | --- |
| **graph wiring** (which pin feeds which) | not decoded yet; `links:` is experimental |
| **which entity a graph is attached to** | graph ids are scoped locally and never referenced elsewhere in the file (probe result recorded in `docs/node-graph-extraction.md`). So "this graph must be on the level entity" can only be a **reminder**, never a verification |
| **most node type names** | only the verified ids are built in (9 today); everything else needs `node-types.txt` |
| **node parameters** | only partially decoded (some default values) |
| compound-node internals | not decoded |

## 6. Conventions you must keep

- **Read-only, always.** Never write, patch or re-encode the input. Do not turn this into a writer:
  publishing a tool that modifies level data is itself a prohibited act under the game's creator
  rules (see the compliance section in the READMEs).
- **Never commit `.gil` files or third-party tables.** `.gitignore` excludes `*.gil`; only derived
  `id<TAB>name` lines belong in the repository.
- **Bilingual docs:** `README.md` and `README.zh-CN.md` are one artefact — change both together.

## 7. Where to read next

- `docs/node-graph-extraction.md` — measured field layout, what is decoded, the ground-truth edge
  table a future wiring decoder must reproduce, and the plan for growing `node-types.txt` from
  official tutorial levels.
- When a check here turns out to duplicate one of the editor's own checks, **delete it** rather
  than maintain it twice.

---

## 中文摘要

这是**只读**的 `.gil` 关卡导出检查工具。拿到本仓库地址后：

1. `go build ./...` 构建（无依赖，Go 1.27+）
2. 四个命令覆盖全部用法：`-path`（盘点+与上次导出对比）｜`-graphs`（节点图纯文本）｜`-lint`（常规检查，含**变量类型**）｜`-scan 目录`（批量 + 类型 id 并集）
3. **`links:` 是实验性的**，还原不出真实连线，**不要**据此画流程图并当成图逻辑
4. **图的挂载主体读不出来** → 「实体销毁事件必须挂关卡实体」这类问题只能**提醒**，无法确证
5. 工具**不替代**编辑器自带的【试玩校验】与【风险检查】；**已验证的盲区**是「自定义变量类型写错」（官方不报）
6. 保持**只读**、**不提交 `.gil` 与第三方表**、**两份 README 一起改**
