// Batch scan: summarise many exports at once.
//
// Purpose (2026-10-05): official tutorial levels ship as .gil downloads, and each tutorial text
// names the nodes it uses. Scanning a whole folder gives us, per file, the graph/node/type and
// variable picture -- and at the end the UNION of node type ids across all files. Cross-reading
// that against the tutorial texts is how the type-id table gets built without guessing or
// borrowing a third-party table.
//
// Read-only, like everything else here.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type scanRow struct {
	Name      string
	Size      int
	Graphs    int
	Nodes     int
	VarTypes  int
	ParseWarn string
}

// scanPath walks a file or directory and prints a summary plus the type-id unions.
func scanPath(root string, graphField int) error {
	paths := []string{}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if info.IsDir() {
		walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries rather than aborting the whole scan
			}
			if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".gil") {
				paths = append(paths, p)
			}
			return nil
		})
		if walkErr != nil {
			return walkErr
		}
		sort.Strings(paths)
	} else {
		paths = append(paths, root)
	}
	if len(paths) == 0 {
		return fmt.Errorf("no .gil files under %s", root)
	}

	nodeTypeUse := map[uint64]int{} // node type id -> occurrences across all files
	varTypeUse := map[uint64]int{}  // variable type id -> declarations across all files
	rows := []scanRow{}

	fmt.Printf("scanning %d file(s) under %s\n\n", len(paths), root)
	fmt.Printf("%-42s %9s %6s %6s %8s\n", "file", "bytes", "graphs", "nodes", "varTypes")
	fmt.Println(strings.Repeat("-", 78))

	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			fmt.Printf("%-42s  <unreadable: %v>\n", filepath.Base(p), err)
			continue
		}
		row := scanRow{Name: filepath.Base(p), Size: len(raw)}
		if payload, ok := fieldPayload(raw, graphField); ok {
			for _, g := range extractGraphs(payload) {
				row.Graphs++
				row.Nodes += len(g.Nodes)
				for _, n := range g.Nodes {
					nodeTypeUse[n.TypeID]++
				}
			}
		} else {
			row.ParseWarn = "no graph field"
		}
		decls := scanVariableDeclarations(raw)
		seenVar := map[uint64]bool{}
		for _, d := range decls {
			varTypeUse[d.TypeID] += d.Sites
			if !seenVar[d.TypeID] {
				seenVar[d.TypeID] = true
				row.VarTypes++
			}
		}
		rows = append(rows, row)
		fmt.Printf("%-42s %9d %6d %6d %8d %s\n", row.Name, row.Size, row.Graphs, row.Nodes, row.VarTypes, row.ParseWarn)
	}

	fmt.Printf("\n=== node type ids across all files (%d distinct) ===\n", len(nodeTypeUse))
	for _, id := range sortedKeys(nodeTypeUse) {
		name := nodeTypeNames[id]
		if name == "" {
			name = fmt.Sprintf("?%d", id)
		}
		fmt.Printf("  %5d  ×%-4d  %s\n", id, nodeTypeUse[id], name)
	}

	fmt.Printf("\n=== variable type ids across all files (%d distinct) ===\n", len(varTypeUse))
	vt := loadVarTypes("var-types.txt")
	for _, id := range sortedKeys(varTypeUse) {
		name := vt[id]
		if name == "" {
			name = "（未映射）"
		}
		fmt.Printf("  %5d  ×%-4d  %s\n", id, varTypeUse[id], name)
	}

	fmt.Println("\nUnmapped node ids are the work list: read the matching tutorial text, then add")
	fmt.Println(`lines "id<TAB>name" to node-types.txt.`)
	return nil
}

// nodeTypeNames is populated from node-types.txt at startup by loadNodeTypes, but scanPath can
// also be called before that; keep a package-level copy for the summary.
var nodeTypeNames = map[uint64]string{}

func sortedKeys(m map[uint64]int) []uint64 {
	out := make([]uint64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return m[out[i]] > m[out[j]] })
	return out
}
