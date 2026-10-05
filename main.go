// Command gil-inspect reports what is inside a Miliastra Wonderland level export (.gil).
//
// STRICTLY READ-ONLY: the input file is only ever opened for reading. This program writes
// exactly two things - a Markdown report and a JSON snapshot of field sizes plus name sets -
// and never patches, rewrites or re-encodes the .gil. See README.md for the reasoning.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Field is one top-level entry of the container payload.
type Field struct {
	N     int    `json:"n"`
	Wire  string `json:"wire"`
	Size  int    `json:"size"`
	Value uint64 `json:"value,omitempty"`
}

// Snapshot is what we remember between runs: sizes and names, never payload bytes.
type Snapshot struct {
	Path    string   `json:"path"`
	Size    int      `json:"size"`
	Written string   `json:"written"`
	Fields  []Field  `json:"fields"`
	CJK     []string `json:"cjk"`
	IDs     []string `json:"ids"`
}

var (
	cjkRe = regexp.MustCompile(`[\p{Han}]{2,}`)
	idRe  = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9_]{2,29}\b`)
)

func main() {
	path := flag.String("path", "", "path to the .gil file (required)")
	keysCSV := flag.String("keys", defaultKeys, "comma-separated keyword list (ASCII)")
	keyFile := flag.String("keyfile", "gil-keys.json", "optional UTF-8 JSON array of extra keywords")
	out := flag.String("out", "", "markdown report path (default <name>.inspect.md)")
	snap := flag.String("snapshot", "", "snapshot path (default <dir>/gil-snapshot.json)")
	graphDump := flag.Bool("graphdump", false, "also dump the node-graph field as an indented Markdown tree")
	graphs := flag.Bool("graphs", false, "write the node graphs as a plain-text/markdown intermediate file")
	pins := flag.Bool("pins", false, "debug: print every pin descriptor in raw form (calibration aid)")
	lint := flag.Bool("lint", false, "write <name>.lint.md - routine sanity checks (dangling references, naming, graph size, variable types)")
	scan := flag.String("scan", "", "walk a .gil file or a directory of them: summary table + type-id unions")
	nodeTypes := flag.String("nodetypes", "node-types.txt", "id<TAB>name lookup table for node types")
	graphField := flag.Int("graphfield", 10, "container field number that holds node graphs")
	flag.Parse()

	if *scan != "" {
		nodeTypeNames = loadNodeTypes(*nodeTypes)
		if err := scanPath(*scan, *graphField); err != nil {
			fmt.Fprintln(os.Stderr, "scan failed:", err)
			os.Exit(1)
		}
		return
	}

	if *path == "" {
		fmt.Fprintln(os.Stderr, "usage: gil-inspect -path <file.gil>   (or -scan <dir> for a batch summary)")
		os.Exit(2)
	}
	if *out == "" {
		*out = strings.TrimSuffix(*path, filepath.Ext(*path)) + ".inspect.md"
	}
	if *snap == "" {
		*snap = filepath.Join(filepath.Dir(*path), "gil-snapshot.json")
	}

	raw, err := os.ReadFile(*path) // read-only, always
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot read file:", err)
		os.Exit(1)
	}
	info, err := os.Stat(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot stat file:", err)
		os.Exit(1)
	}

	keys := splitCSV(*keysCSV)
	keys = append(keys, loadKeyFile(*keyFile)...)

	header := containerHeader(raw)
	fields, walkErr := parseFields(raw, 20)
	text := string(raw)
	cjk := uniqSorted(matches(cjkRe, text))
	ids := uniqSorted(matches(idRe, text))

	prev := loadSnapshot(*snap)
	sizeDiffs, newCJK, goneCJK := diff(prev, fields, cjk)

	// snapshot: names and sizes only
	cur := Snapshot{
		Path:    filepath.Base(*path),
		Size:    len(raw),
		Written: time.Now().Format("2006-01-02T15:04:05"),
		Fields:  fields,
		CJK:     cjk,
		IDs:     ids,
	}
	if blob, err := json.MarshalIndent(cur, "", "  "); err == nil {
		if err := os.WriteFile(*snap, blob, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "cannot write snapshot:", err)
		}
	}

	report := buildReport(*path, info, len(raw), header, fields, walkErr, keys, sizeDiffs, newCJK, goneCJK, cjk, ids)
	if err := os.WriteFile(*out, []byte(report), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "cannot write report:", err)
		os.Exit(1)
	}

	if *graphDump {
		base := strings.TrimSuffix(*path, filepath.Ext(*path))
		treePath := fmt.Sprintf("%s.graph-f%d.md", base, *graphField)
		tree, ok := dumpField(raw, *graphField, filepath.Base(*path))
		if !ok {
			fmt.Fprintf(os.Stderr, "field %d not found (or is not length-delimited)\n", *graphField)
		} else if err := os.WriteFile(treePath, []byte(tree), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "cannot write graph dump:", err)
		} else {
			fmt.Printf("graph dump: %s\n", treePath)
		}
	}

	if *graphs {
		base := strings.TrimSuffix(*path, filepath.Ext(*path))
		outPath := fmt.Sprintf("%s.graphs.md", base)
		if payload, ok := fieldPayload(raw, *graphField); ok {
			gs := extractGraphs(payload)
			table := loadNodeTypes(*nodeTypes)
			report := renderGraphs(filepath.Base(*path), gs, table)
			if err := os.WriteFile(outPath, []byte(report), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, "cannot write graphs file:", err)
			} else {
				total, unknown := 0, map[uint64]bool{}
				for _, g := range gs {
					total += len(g.Nodes)
					for _, n := range g.Nodes {
						if table[n.TypeID] == "" {
							unknown[n.TypeID] = true
						}
					}
				}
				fmt.Printf("graphs    : %s (%d graphs, %d nodes, %d unmapped types)\n", outPath, len(gs), total, len(unknown))
			}
		} else {
			fmt.Fprintf(os.Stderr, "field %d not found (or is not length-delimited)\n", *graphField)
		}
	}

	if *pins {
		base := strings.TrimSuffix(*path, filepath.Ext(*path))
		outPath := fmt.Sprintf("%s.pins.md", base)
		if payload, ok := fieldPayload(raw, *graphField); ok {
			if err := os.WriteFile(outPath, []byte(dumpPins(payload)), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, "cannot write pin dump:", err)
			} else {
				fmt.Printf("pin dump  : %s\n", outPath)
			}
		}
	}

	if *lint {
		base := strings.TrimSuffix(*path, filepath.Ext(*path))
		outPath := fmt.Sprintf("%s.lint.md", base)
		findings := lintExport(raw, *graphField)
		names := []string{}
		if payload, ok := fieldPayload(raw, *graphField); ok {
			for _, g := range extractGraphs(payload) {
				if g.Name != "" {
					names = append(names, g.Name)
				}
			}
		}
		varTypes := loadVarTypes("var-types.txt")
		decls := scanVariableDeclarations(raw)
		report := renderLint(filepath.Base(*path), findings, names) + renderVarTable(decls, varTypes)
		if err := os.WriteFile(outPath, []byte(report), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "cannot write lint report:", err)
		} else {
			errs, warns := 0, 0
			for _, f := range findings {
				switch f.Level {
				case "error":
					errs++
				case "warn":
					warns++
				}
			}
			fmt.Printf("lint      : %s (%d error, %d warn)\n", outPath, errs, warns)
		}
	}

	// console summary
	fmt.Printf("file      : %s\n", info.Name())
	fmt.Printf("size      : %d bytes\n", len(raw))
	fmt.Printf("fields    : %d\n", len(fields))
	fmt.Printf("cjk / ids : %d / %d\n", len(cjk), len(ids))
	if len(sizeDiffs) > 0 {
		fmt.Println("size changes vs snapshot:")
		for _, d := range sizeDiffs {
			fmt.Printf("  f%-3d %6d -> %6d (%+d)\n", d.Field, d.Was, d.Now, d.Delta)
		}
	}
	fmt.Println("keyword hits:")
	for _, k := range keys {
		if n := strings.Count(text, k); n > 0 {
			fmt.Printf("  %-16s %d\n", k, n)
		}
	}
	fmt.Printf("report    : %s\nsnapshot  : %s\n", *out, *snap)
	if walkErr != "" {
		fmt.Println("WARNING: field walk stopped early:", walkErr)
	}
}

const defaultKeys = "spawn_unit,spawn_interval,spawn_timer,spawnTimer,spawnUnit,isBase,is_base,slot_id,slotId,race,raceA,raceB,racePicked"

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func loadKeyFile(path string) []string {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var extra []string
	if err := json.Unmarshal(blob, &extra); err != nil {
		fmt.Fprintln(os.Stderr, "ignoring unreadable key file:", path)
		return nil
	}
	return extra
}

// readVarint decodes a base-128 varint. NOTE: never name the parameter b and a local B -
// Go is case-sensitive, so that specific trap is not a problem here (it was in the
// PowerShell version of this tool, where variable names are case-INsensitive).
func readVarint(buf []byte, pos *int) (uint64, error) {
	var val uint64
	var shift uint
	for {
		if *pos >= len(buf) {
			return 0, fmt.Errorf("unexpected end of file")
		}
		c := buf[*pos]
		*pos = *pos + 1
		val |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return val, nil
		}
		shift += 7
		if shift > 63 {
			return 0, fmt.Errorf("varint too long")
		}
	}
}

// parseFields walks the top-level protobuf entries of the payload (which starts at offset 20).
func parseFields(buf []byte, start int) ([]Field, string) {
	out := []Field{}
	pos := start
	for pos < len(buf) {
		key, err := readVarint(buf, &pos)
		if err != nil {
			return out, err.Error()
		}
		n := int(key >> 3)
		wire := int(key & 7)
		if n <= 0 {
			return out, fmt.Sprintf("field 0 / invalid key at offset %d", pos)
		}
		switch wire {
		case 0:
			v, err := readVarint(buf, &pos)
			if err != nil {
				return out, err.Error()
			}
			out = append(out, Field{N: n, Wire: "varint", Value: v})
		case 2:
			length, err := readVarint(buf, &pos)
			if err != nil {
				return out, err.Error()
			}
			size := int(length)
			out = append(out, Field{N: n, Wire: "bytes", Size: size})
			pos += size
		case 5:
			out = append(out, Field{N: n, Wire: "fixed32", Size: 4})
			pos += 4
		case 1:
			out = append(out, Field{N: n, Wire: "fixed64", Size: 8})
			pos += 8
		default:
			return out, fmt.Sprintf("unsupported wire type %d for field %d at offset %d", wire, n, pos)
		}
		if pos > len(buf) {
			return out, "payload runs past end of file"
		}
	}
	return out, ""
}

// containerHeader decodes the five big-endian 32-bit words that precede the payload.
func containerHeader(buf []byte) []uint32 {
	out := []uint32{}
	for i := 0; i+3 < len(buf) && i < 20; i += 4 {
		out = append(out, uint32(buf[i])<<24|uint32(buf[i+1])<<16|uint32(buf[i+2])<<8|uint32(buf[i+3]))
	}
	return out
}

func matches(re *regexp.Regexp, s string) []string {
	found := re.FindAllString(s, -1)
	return found
}

func uniqSorted(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

type sizeDiff struct {
	Field, Was, Now, Delta int
}

func loadSnapshot(path string) *Snapshot {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s Snapshot
	if err := json.Unmarshal(blob, &s); err != nil {
		return nil
	}
	return &s
}

func diff(prev *Snapshot, fields []Field, cjk []string) ([]sizeDiff, []string, []string) {
	if prev == nil {
		return nil, nil, nil
	}
	was := map[int]int{}
	for _, f := range prev.Fields {
		was[f.N] = f.Size
	}
	diffs := []sizeDiff{}
	for _, f := range fields {
		if f.Wire != "bytes" {
			continue
		}
		if old, ok := was[f.N]; ok && old != f.Size {
			diffs = append(diffs, sizeDiff{Field: f.N, Was: old, Now: f.Size, Delta: f.Size - old})
		}
	}
	oldCJK := map[string]bool{}
	for _, v := range prev.CJK {
		oldCJK[v] = true
	}
	nowCJK := map[string]bool{}
	for _, v := range cjk {
		nowCJK[v] = true
	}
	added, removed := []string{}, []string{}
	for _, v := range cjk {
		if !oldCJK[v] {
			added = append(added, v)
		}
	}
	for _, v := range prev.CJK {
		if !nowCJK[v] {
			removed = append(removed, v)
		}
	}
	return diffs, added, removed
}

func cell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

func buildReport(path string, info os.FileInfo, size int, header []uint32, fields []Field, walkErr string, keys []string, diffs []sizeDiff, added, removed, cjk, ids []string) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	w("# gil-inspect report")
	w("")
	w("> read-only report; the input file was never modified")
	w("")
	w("| item | value |")
	w("| --- | --- |")
	w("| file | %s |", cell(path))
	w("| size | %d bytes |", size)
	w("| modified | %s |", info.ModTime().Format("2006-01-02 15:04:05"))
	for i, h := range header {
		w("| header @%d | %d (= size - %d) |", i*4, h, size-int(h))
	}
	w("")
	w("## top-level protobuf fields")
	w("")
	if walkErr != "" {
		w("> WARNING: field walk stopped early - %s", walkErr)
		w("")
	}
	w("| field | wire | size | value |")
	w("| --- | --- | --- | --- |")
	for _, f := range fields {
		value := ""
		if f.Wire == "varint" {
			value = fmt.Sprint(f.Value)
		}
		w("| f%d | %s | %d | %s |", f.N, f.Wire, f.Size, value)
	}
	w("")
	if len(diffs) > 0 {
		w("## size changes vs snapshot")
		w("")
		w("| field | was | now | delta |")
		w("| --- | --- | --- | --- |")
		for _, d := range diffs {
			w("| f%d | %d | %d | %+d |", d.Field, d.Was, d.Now, d.Delta)
		}
		w("")
	}
	if added != nil || removed != nil {
		w("## new / removed strings vs snapshot")
		w("")
		w("- new CJK: %s", strings.Join(added, ", "))
		w("- removed CJK: %s", strings.Join(removed, ", "))
		w("")
	}
	w("## keyword checklist")
	w("")
	w("| key | hits |")
	w("| --- | --- |")
	text := ""
	if blob, err := os.ReadFile(path); err == nil {
		text = string(blob)
	}
	for _, k := range keys {
		w("| `%s` | %d |", cell(k), strings.Count(text, k))
	}
	w("")
	w("## CJK strings (%d)", len(cjk))
	w("")
	for _, s := range cjk {
		w("- %s", s)
	}
	w("")
	w("## identifiers (%d)", len(ids))
	w("")
	for _, s := range ids {
		w("- `%s`", s)
	}
	return b.String()
}
