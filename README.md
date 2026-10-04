# gil-inspect

A **read-only** inspector for level-export files produced by the official *export save* feature
of the Genshin Impact UGC editor (Miliastra Wonderland / 千星沙箱).

It answers questions about **your own export**: which elements, custom variables and node graphs
are actually in the file, what changed since the last export, and — with a lookup table — what
each node in a graph is called.

```
gil-inspect -path export/MyLevel.gil -graphs
```

## Status

| capability | state |
| --- | --- |
| container header, top-level field map, compression check | ✅ works |
| diff against the previous snapshot (field sizes, added/removed names) | ✅ works |
| keyword checklist (which of your identifiers are present, and how often) | ✅ works |
| CJK strings / ASCII identifiers | ✅ works |
| **node graphs**: graph names, node list, **node type ids**, positions, custom titles, referenced variable names | ✅ works |
| node type **names** | ✅ works *with* a `node-types.txt` lookup table (not bundled) |
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
