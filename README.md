# gil-inspect

A **read-only** inspector for level-export files produced by the official
"export save" feature of the *Genshin Impact* UGC editor (Miliastra Wonderland / 千星沙箱).

It tells you **what is inside your own export** — element names, custom-variable names,
node-graph names, and which of your identifiers are present — without ever modifying the file.

```
gil-inspect -path export/MyLevel.gil
```

## Why read-only, and why that is not negotiable

The game's rules for creators forbid *"creating, publishing, distributing, using or
advocating any auxiliary tool or program that simulates user actions, changes the operating
environment, or modifies data"* in a way that harms fairness.

Therefore this tool:

- **never writes, rewrites or patches the `.gil` file** — it only reads it;
- **never touches the game's installation directory, cache or runtime**;
- **never automates the editor** (no simulated clicks, no OCR, no injection);
- writes only a Markdown **report** and a JSON **snapshot of identifier names**.

> ⚠️ **Please do not fork this into a writer.** A tool that *modifies* level data crosses the
> line described above — and publishing such a tool is itself the prohibited act.

## What it reports

| section | content |
| --- | --- |
| container | file size, the 5-word header, compression check |
| top-level fields | protobuf field table (number / wire type / payload size) |
| diff | field-size changes **and** added/removed names versus the previous snapshot |
| keywords | hit counts for the identifiers you care about |
| strings | extracted CJK strings and ASCII identifiers |

The snapshot contains **only field sizes and name sets** — never payload bytes.

## Usage

```
# basic
gil-inspect -path export/MyLevel.gil

# custom keywords (ASCII) + extra keywords from a UTF-8 JSON array file
gil-inspect -path export/MyLevel.gil -keys spawn_unit,isBase -keyfile keys.json

# explicit outputs
gil-inspect -path export/MyLevel.gil -out report.md -snapshot prev.json
```

Defaults: report goes next to the `.gil` as `<name>.inspect.md`, snapshot as
`<dir>/gil-snapshot.json`.

## Build

```
go build ./...
go vet ./...
```

Requires Go 1.27+. Standard library only — no dependencies.

## Disclaimer

Unofficial, community-made tool. Not affiliated with or endorsed by HoYoverse / miHoYo.
`Miliastra Wonderland`, `Genshin Impact`, and the `.gil` format are the property of their
respective owners. This project ships **no** game content, assets or official documentation.
Use it only on files you exported yourself, and check your local rules before redistributing
anything derived from them.

## License

MIT — see [LICENSE](LICENSE).
