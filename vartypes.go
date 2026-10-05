// Custom-variable declarations and their declared types.
//
// Why this exists (2026-10-05): a building's custom variable meant to hold an 元件ID (element
// id) was declared as 整数 instead. Nothing errored -- the spawner simply did nothing -- and it
// cost a debugging session. Type mismatches in this platform are silent, so they deserve a lint.
//
// Measured layout of one declaration (empirically, on a real export):
//
//	0a <len>          field 1: the variable entry
//	  12 <len> <name> field 2: the name
//	  18 <varint>     field 3: THE TYPE ID
//	  22 <len> ...    field 4: a descriptor repeating the type
//	  a2 01 <len> ... field 20: default value (encoding depends on the type; float32 for 浮点)
//
// Graph names share the same 0x12 <len> <name> prefix but are followed by 0x1a (the node list)
// instead of 0x18, which is what keeps declarations and graph names apart.
//
// Type ids are internal, so only ids confirmed against a known variable are named here; the rest
// are printed as numbers and can be filled in via var-types.txt -- the same pattern as
// node-types.txt, so no third-party table is redistributed with this repository.
package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// VarDecl is one declared custom variable.
type VarDecl struct {
	Name    string
	TypeID  uint64
	Sites   int    // how many declaration sites carry this exact (name, type)
	Default string // rendered default value when one was found
}

// Type ids verified against variables whose type we know from building the level.
// Deliberately tiny: an unverified guess here would produce confident nonsense.
var knownVarTypes = map[uint64]string{
	4:  "布尔",
	10: "浮点",
	21: "元件ID",
}

// names that suggest a *reference* (element id / template / config) rather than a number
var referenceNameHints = []string{"_id", "id_", "_unit", "unit_", "_元件", "_模板", "_配置", "元件", "模板"}

// numeric types: storing an element id in one of these is the bug this lint is about
var numericVarTypes = map[uint64]bool{2: true, 3: true, 10: true, 11: true}

// loadVarTypes reads extra "id<TAB>name" lines, mirroring node-types.txt.
func loadVarTypes(path string) map[uint64]string {
	out := map[uint64]string{}
	for id, name := range knownVarTypes {
		out[id] = name
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		id, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			continue
		}
		out[id] = parts[1]
	}
	return out
}

func typeLabel(id uint64, table map[uint64]string) string {
	if name, ok := table[id]; ok {
		return fmt.Sprintf("%s(%d)", name, id)
	}
	return fmt.Sprintf("type %d（未映射）", id)
}

// scanVariableDeclarations finds every custom-variable declaration in the container.
func scanVariableDeclarations(raw []byte) []VarDecl {
	type key struct {
		name string
		id   uint64
	}
	order := []key{}
	counts := map[key]int{}
	defaults := map[key]string{}

	for pos := 0; pos+4 < len(raw); pos++ {
		if raw[pos] != 0x12 {
			continue
		}
		length, used, ok := readVarintAt(raw, pos+1)
		if !ok || length < 2 || length > 60 {
			continue
		}
		start := pos + 1 + used
		end := start + int(length)
		if end >= len(raw) || raw[end] != 0x18 {
			continue
		}
		name := string(raw[start:end])
		if !isTexty(raw[start:end]) || strings.ContainsAny(name, "\r\n\t") {
			continue
		}
		typeID, typeUsed, ok := readVarintAt(raw, end+1)
		if !ok || typeID > 200 {
			continue
		}
		k := key{name: name, id: typeID}
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
		if v, ok := defaultAfter(raw[end+1+typeUsed:]); ok && defaults[k] == "" {
			defaults[k] = v
		}
	}

	out := make([]VarDecl, 0, len(order))
	for _, k := range order {
		out = append(out, VarDecl{Name: k.name, TypeID: k.id, Sites: counts[k], Default: defaults[k]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].TypeID < out[j].TypeID
	})
	return out
}

func readVarintAt(b []byte, pos int) (uint64, int, bool) {
	var val uint64
	var shift uint
	for i := pos; i < len(b) && i < pos+10; i++ {
		val |= uint64(b[i]&0x7f) << shift
		if b[i]&0x80 == 0 {
			return val, i - pos + 1, true
		}
		shift += 7
	}
	return 0, 0, false
}

// defaultAfter finds the field-20 default value that follows a declaration and renders it.
// The default is nested inside the field-4 descriptor, so instead of assuming a byte layout we
// search a small window for the "0a 04 <float32>" shape and read that.
func defaultAfter(b []byte) (string, bool) {
	if len(b) > 32 {
		b = b[:32]
	}
	for i := 0; i+6 <= len(b); i++ {
		if b[i] != 0x0a || b[i+1] != 0x04 {
			continue
		}
		bits := uint32(b[i+2]) | uint32(b[i+3])<<8 | uint32(b[i+4])<<16 | uint32(b[i+5])<<24
		f := math.Float32frombits(bits)
		// reject shapes that are obviously not a plausible default value
		if f != f || f > 1e9 || f < -1e9 {
			continue
		}
		return fmt.Sprintf("%g", f), true
	}
	return "", false
}

func looksLikeReferenceName(name string) bool {
	lower := strings.ToLower(name)
	for _, h := range referenceNameHints {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}

// lintVariables appends type-related findings.
func lintVariables(raw []byte, types map[uint64]string, add func(level, code, what string)) []VarDecl {
	decls := scanVariableDeclarations(raw)

	// same name declared with different types in different places
	byName := map[string]map[uint64]int{}
	for _, d := range decls {
		if byName[d.Name] == nil {
			byName[d.Name] = map[uint64]int{}
		}
		byName[d.Name][d.TypeID] += d.Sites
	}
	for name, ids := range byName {
		if len(ids) < 2 {
			continue
		}
		parts := []string{}
		for id, n := range ids {
			parts = append(parts, fmt.Sprintf("%s ×%d", typeLabel(id, types), n))
		}
		sort.Strings(parts)
		add("error", "variable-type-conflict",
			fmt.Sprintf("变量 `%s` 在不同位置被声明成了**不同类型**：%s —— 同名不同型必然有一处写错，运行时行为不可预期", name, strings.Join(parts, "、")))
	}

	for _, d := range decls {
		// a name that reads like a reference must not be declared as a number
		if looksLikeReferenceName(d.Name) && numericVarTypes[d.TypeID] {
			add("warn", "reference-typed-as-number",
				fmt.Sprintf("变量 `%s` 声明为 %s，但名字暗示它应存**引用**（元件ID / 模板 等）—— "+
					"这正是「类型写成整数导致机制静默不生效」的那类问题，请改用 元件ID 类型", d.Name, typeLabel(d.TypeID, types)))
		}
		// a float default under a non-float type is a contradiction
		if d.Default != "" && d.TypeID != 10 && d.TypeID != 11 {
			add("warn", "default-value-type-mismatch",
				fmt.Sprintf("变量 `%s` 声明为 %s，却带着一个浮点默认值 %s —— 默认值与声明类型不一致", d.Name, typeLabel(d.TypeID, types), d.Default))
		}
		if _, known := types[d.TypeID]; !known {
			add("info", "unmapped-variable-type",
				fmt.Sprintf("变量 `%s` 的类型 id %d 尚未映射（加到 var-types.txt 即可命名）", d.Name, d.TypeID))
		}
	}
	return decls
}

// renderVarTable renders the declared-variable inventory for the lint report.
func renderVarTable(decls []VarDecl, types map[uint64]string) string {
	if len(decls) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## 已声明的自定义变量\n\n")
	b.WriteString("| 变量 | 类型 | 声明处 | 默认值 |\n| --- | --- | --- | --- |\n")
	for _, d := range decls {
		label := types[d.TypeID]
		if label == "" {
			label = fmt.Sprintf("type %d（未映射）", d.TypeID)
		}
		def := d.Default
		if def == "" {
			def = "—"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %d | %s |\n", d.Name, label, d.Sites, def)
	}
	return b.String()
}
