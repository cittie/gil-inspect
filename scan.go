// 批量扫描：一次汇总多个导出文件。
//
// 目的（2026-10-05）：官方教程关卡以 .gil 形式提供下载，而每篇教程正文都写明了它用了哪些节点。
// 扫一个目录就能得到每个文件的图 / 节点 / 类型与变量概况，最后再给出**全部文件节点类型 id 的并集**。
// 拿这个并集去对照教程正文，就能**不靠猜、也不转发第三方映射表**地把类型表建起来。
//
// 与其它部分一样：**只读**。
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

// scanExtensions：关卡存档（.gil）与资产文件（.gia）**容器格式相同** ——
// 20 字节头 + protobuf —— 所以一个扫描器覆盖两者。
var scanExtensions = map[string]bool{".gil": true, ".gia": true}

func isScanCandidate(name string) bool {
	return scanExtensions[strings.ToLower(filepath.Ext(name))]
}

// scanPath 走查一个文件或目录，打印汇总表与类型 id 并集。
func scanPath(root string, graphField int) error {
	paths := []string{}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if info.IsDir() {
		walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // 跳过读不了的条目，而不是让整次扫描失败
			}
			if !d.IsDir() && isScanCandidate(p) {
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
		return fmt.Errorf("no .gil / .gia files under %s", root)
	}

	nodeTypeUse := map[uint64]int{} // 节点类型 id -> 全部文件中的出现次数
	varTypeUse := map[uint64]int{}  // 变量类型 id -> 全部文件中的声明次数
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
		if gs, ok := graphsFrom(raw, graphField); ok {
			for _, g := range gs {
				row.Graphs++
				row.Nodes += len(g.Nodes)
				for _, n := range g.Nodes {
					if n.TypeID < refTypeBase && n.TypeID != assetBoundaryType {
						nodeTypeUse[n.TypeID]++
					}
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

// nodeTypeNames 由 loadNodeTypes 在启动时从 node-types.txt 填入；
// 但 scanPath 也可能在那之前被调用，所以这里保留一份包级副本供汇总使用。
var nodeTypeNames = map[uint64]string{}

func sortedKeys(m map[uint64]int) []uint64 {
	out := make([]uint64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return m[out[i]] > m[out[j]] })
	return out
}
