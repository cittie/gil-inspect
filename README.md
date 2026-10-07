# gil-inspect

**English** ｜ [中文](README.zh-CN.md)

> 🤖 **AI assistants: read [`AGENTS.md`](AGENTS.md) first.** It maps this URL straight to the
> commands, the files they write, how to interpret them, and what the tool cannot tell you.

A **read-only** inspector for level-export files produced by the official *export save* feature
of the Genshin Impact UGC editor (Miliastra Wonderland / 千星沙箱).

It answers questions about **your own export**: which elements, custom variables and node graphs
are actually in the file, what changed since the last export, and — with a lookup table — what
each node in a graph is called.

```
gil-inspect -path export/MyLevel.gil -graphs
```

> **Bilingual docs.** This README and [`README.zh-CN.md`](README.zh-CN.md) are maintained together —
> when you change one, update the other in the same commit.

## Status

| capability | state |
| --- | --- |
| container header, top-level field map, compression check | ✅ works |
| diff against the previous snapshot (field sizes, added/removed names) | ✅ works |
| keyword checklist (which of your identifiers are present, and how often) | ✅ works |
| CJK strings / ASCII identifiers | ✅ works |
| **node graphs**: graph names, node list, **node type ids**, positions, custom titles, referenced variable names | ✅ works |
| node type **names** | ✅ **30 ids** mapped: 9 verified against a real graph and built in, 21 more in the shipped `node-types.txt`; every name cross-checked against the official node catalogue. Our own level now reports **0 unmapped types** |
| graph **wiring** (which pin feeds which) | ⚠️ **experimental, not trustworthy** — see below |

### Wiring is explicitly not claimed

A first decoding pass produces `links:` in the intermediate file, but measured against a graph
whose wiring is known from a screenshot it reproduces only 2 of 14 real edges and invents others.
The pin blocks decoded so far simply do not contain the real node pairs. The section is therefore
labelled `EXPERIMENTAL` in the output, and the ground truth a future decoder must match is written
down in [docs/node-graph-extraction.md](docs/node-graph-extraction.md).

## Usage

```
gil-inspect -path <file.gil> [flags]

  -graphs          write <name>.graphs.md  - the plain-text intermediate file
  -lint            write <name>.lint.md    - routine sanity checks (see below)
  -scan DIR        walk a folder of exports (.gil) and asset files (.gia): summary + type-id unions
  -nodetypes FILE  type lookup table (default node-types.txt)
  -graphfield N    container field holding node graphs (default 10)
  -graphdump       write <name>.graph-fN.md - full indented tree (decoding aid)
  -pins            write <name>.pins.md     - raw pin descriptors (calibration aid)
  -keys a,b,c      keyword list (ASCII); extra keys can live in a JSON array file
  -keyfile FILE    default gil-keys.json
  -out FILE        report path (default <name>.inspect.md)
  -snapshot FILE   default <dir>/gil-snapshot.json
```

Typical workflow:

```
gil-inspect -path export/Level.gil                    # inventory + diff vs last export
gil-inspect -path export/Level.gil -graphs            # readable node-graph intermediate
```

The intermediate file is deliberately **plain markdown** (no JSON) and drops positions, node ids
where irrelevant, and everything else that gets in the way when a human or an AI reads it:

```
## 关卡-建筑销毁

nodes:
  1	终止定时器	"测试出兵定时器"
  2	查询自定义变量快照	"isBase"
  3	实体销毁时
  4	双分支
  10	多分支	"4"	refs: 5
links:
  # EXPERIMENTAL - not verified, do not trust yet
```

## Routine checks (`-lint`)

> 🚫 **Not a replacement for the editor's own checks.** The platform ships two:
> **试玩校验** (playtest validation — blocking errors that stop a playtest) and
> **风险检查** (risk check — non-blocking prompts about discouraged configuration or hidden
> rules, usually data-level mistakes). **Whatever they report is authoritative**; `-lint`
> deliberately aims at what they do *not* see: naming conventions, consistency across
> separately-configured places, and the state of the file itself. If a check here turns out to
> duplicate a built-in one, it should be dropped rather than maintained twice.
>
> ✅ **Verified blind spot (2026-10-05):** declaring a custom variable with the wrong **type**
> (e.g. an element id stored in an 整数) is **not** reported by 风险检查 — the mechanism just
> silently does nothing. The variable-type checks below exist for exactly that gap.

Static checks that need no input from you:

| check | what it looks for |
| --- | --- |
| `dangling-reference` | an identifier that **only ever appears in the node-graph section** — if it is a variable name, nothing declares it (the exact "used in a graph but never configured on an element or the level" case) |
| `reference-typed-as-number` | a variable whose name reads like a **reference** (`*_unit`, `*_id`, 元件, 模板) but is declared as a **numeric** type — this is the "element id stored in an integer" bug, where the mechanism silently does nothing |
| `variable-type-conflict` | the **same variable name declared with different types** in different places |
| `default-value-type-mismatch` | the declared type and the encoded default value disagree |
| `unmapped-variable-type` | a type id with no name yet (add it to `var-types.txt`; harmless on its own) |
| `graph-only-name` | a CJK name living only in graphs: suspicious if it is a variable, normal if it is a compound node, a node's custom title or a timer name |
| `similar-names` | one name is a prefix of another (easy to confuse, e.g. `选阵营` vs `选完阵营`) |
| `naming-style-mix` | snake_case and camelCase used side by side |
| `banned-name` | names that read as "my side / enemy side" — the architecture is symmetric |
| `graph-large` / `empty-graph` | per-graph node counts against the platform limits |
| `parse-health` | the top-level field walk stopped well before the end of the file |

Two things to keep in mind:

- every finding is a **thing to verify**, not a verdict — the container simply does not carry
  enough context to be certain (an identifier seen only in graphs may legitimately be a graph
  name or a timer name);
- nothing is ever auto-fixed, and there is no "apply suggestion" mode. The tool stays read-only.

The report opens with the graph names it recognised, because those are expected to appear only
in the graph section and would otherwise look like findings.

It also ends with an inventory of **every declared custom variable** — name, declared type,
how many declaration sites carry it, and its default value when one is stored:

```
| variable | type | sites | default |
| spawn_interval | 浮点 | 12 | 12 |
| spawn_unit | 元件ID | 12 | — |
| isBase | 布尔 | 4 | — |
```

Type ids are internal. Three are verified against variables whose types we know from building
the level — **4 = 布尔, 10 = 浮点, 21 = 元件ID** — and the rest print as `type N（未映射）`;
add them to `var-types.txt` (same shape as `node-types.txt`, not bundled here).

## Node type names: `node-types.txt`

Node types are stored as internal ids (`3360`, `82`, …) and the names are **not in the file**, so
the tool reads a simple lookup table you supply — `id<TAB>name`, `#` comments allowed:

```
3360	查询自定义变量快照
373	实体销毁时
```

The table is **not bundled with this repository** on purpose: you choose the source, and nothing
third-party is redistributed here. Every generated `.graphs.md` ends with the ids that are still
unmapped plus how often each is used, so the table can be grown in batches.

Unmapped ids render as `?3360` rather than being hidden.

## Compliance boundary (please keep it)

The creator rules for this game forbid *"creating, publishing, distributing, using or advocating
any auxiliary tool or program that simulates user actions, changes the operating environment, or
modifies data"* in a way that harms fairness. So this tool:

- **never writes, rewrites or patches the `.gil`** — the input is opened for reading only;
- **never touches the game's installation directory, cache or runtime**;
- **never automates the editor** (no simulated input, no OCR, no injection);
- writes only reports: a Markdown report, a Markdown graph file, and a JSON snapshot of field
  sizes plus identifier **names** (never payload bytes).

> ⚠️ **Please do not fork this into a writer.** A tool that *modifies* level data crosses the line
> above — and publishing such a tool is itself the prohibited act.

## Build / test

```
go build ./...
go test ./...
go vet ./...
```

Requires Go 1.27+. Standard library only — no dependencies.

## Disclaimer

Unofficial, community-made tool. Not affiliated with or endorsed by HoYoverse / miHoYo.
`Miliastra Wonderland`, `Genshin Impact` and the `.gil` format belong to their respective owners.
This project ships **no** game content, assets or official documentation. Use it only on files you
exported yourself.

## License

MIT — see [LICENSE](LICENSE).
