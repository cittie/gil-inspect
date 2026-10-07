// 自定义变量的声明，以及它们声明的类型。
//
// 为什么有这个（2026-10-05）：建筑上一个本该存【元件ID】的自定义变量，第一次被声明成了【整数】。
// **没有任何报错** —— 出兵机制就是"什么都不做" —— 为此多花了一轮排查。这个平台的类型不匹配
// 是**静默**的，所以值得专门做一条 lint。
//
// 一条声明的实测布局（在真实导出上量出来的）：
//
//	0a <len>          字段 1：变量条目
//	  12 <len> <名字>  字段 2：名字
//	  18 <varint>     字段 3：**类型 id**
//	  22 <len> ...    字段 4：重复一遍类型描述符
//	  a2 01 <len> ... 字段 20：默认值（编码方式随类型而变；浮点用 float32）
//
// 图名共享同样的 `0x12 <len> <名字>` 前缀，但后面跟的是 0x1a（节点列表）而不是 0x18 ——
// 这就是把"变量声明"和"图名"区分开的依据。
//
// 类型 id 是内部的，所以这里只命名**用已知变量确认过**的 id；其余按数字打印，可通过
// var-types.txt 补充 —— 与 node-types.txt 同一套做法，因此本仓库不转发任何第三方映射表。
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

// VarDecl 是一个已声明的自定义变量。
type VarDecl struct {
	Name    string
	TypeID  uint64
	Sites   int    // 有多少处声明带着这个完全相同的（名字, 类型）
	Default string // 找到默认值时，渲染成字符串的默认值
}

// 这些类型 id 是用"建房时已知类型的变量"验证过的。
// 刻意保持很小：这里放一条未经验证的猜测，就会产出"自信的胡说"。
var knownVarTypes = map[uint64]string{
	4:  "布尔",
	10: "浮点",
	21: "元件ID",
}

// 名字暗示它是**引用**（元件ID / 模板 / 配置），而不是一个数值
var referenceNameHints = []string{"_id", "id_", "_unit", "unit_", "_元件", "_模板", "_配置", "元件", "模板"}

// 数值类型：把元件ID 存进这几种类型，正是本条 lint 要抓的坑
var numericVarTypes = map[uint64]bool{2: true, 3: true, 10: true, 11: true}

// loadVarTypes 读取额外的 "id<TAB>名字" 行，与 node-types.txt 同一套格式。
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

// scanVariableDeclarations 找出容器里所有的自定义变量声明。
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

// defaultAfter 找出声明后面那个字段 20 的默认值并渲染出来。
// 默认值是**嵌在字段 4 的描述符里面**的，所以这里不假设字节布局，
// 而是在一个小窗口里找 "0a 04 <float32>" 这个形状，然后读出来。
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
		// 排除那些明显不像合理默认值的形状
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

// lintVariables 追加与类型有关的检查结果。
func lintVariables(raw []byte, types map[uint64]string, add func(level, code, what string)) []VarDecl {
	decls := scanVariableDeclarations(raw)

	// 同名变量在不同位置被声明成了不同类型
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
		// 读起来像"引用"的名字，不应该被声明成数值类型
		if looksLikeReferenceName(d.Name) && numericVarTypes[d.TypeID] {
			add("warn", "reference-typed-as-number",
				fmt.Sprintf("变量 `%s` 声明为 %s，但名字暗示它应存**引用**（元件ID / 模板 等）—— "+
					"这正是「类型写成整数导致机制静默不生效」的那类问题，请改用 元件ID 类型", d.Name, typeLabel(d.TypeID, types)))
		}
		// 非浮点类型却带着浮点默认值 —— 自相矛盾
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

// renderVarTable 为检查报告渲染"已声明的变量"清单。
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
