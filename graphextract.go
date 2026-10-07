// 节点图提取：把节点图字段转成纯文本 / Markdown 中间文件。
//
// 一张图的逆向结构（容器字段 10，每张图一个顶层 f1）：
//
//	f1  message            图
//	  f1  message          图体
//	    f1 message         头部（f1 是 id，f2 是容器 id，f3 未知，f5 类似版本号 0x40000005）
//	    f2 text            图名
//	    f3 message         一个**节点条目**，重复出现
//	      f1 varint          节点索引（1、2、3… 连续）
//	      f2 message         节点记录 -> 其 f1/f2/f3 是各种 id，**f5 = 节点类型 id**
//	      f3 message         节点记录的副本
//	      f4 message         引脚 / 连线描述符（内部引用小整数索引）
//	      f5 fixed32         位置 X（float32）
//	      f6 fixed32         位置 Y（float32）
//	      （更深一层）f105.f1 text   可选的节点自定义标题
//
// 节点类型 id 是内部的（82、250、3360 …），而**名字不在文件里**，所以从 node-types.txt
// 读取外部映射表（每行一个 "id<TAB>名字"）。未映射的按 "type N" 报告，并在文末列出，
// 方便映射表逐步长大。
package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

type pbField struct {
	N      int
	Wire   int
	Varint uint64
	Bytes  []byte
}

// parseMessage 把一条 protobuf 消息解成字段列表。若 b 不能**完整、干净地**解析则返回 false
// —— 这正是我们区分"消息"与"不透明载荷 / 文本"的依据。
func parseMessage(b []byte) ([]pbField, bool) {
	out := []pbField{}
	pos := 0
	for pos < len(b) {
		key, err := readVarint(b, &pos)
		if err != nil {
			return nil, false
		}
		n := int(key >> 3)
		wire := int(key & 7)
		if n <= 0 || n > 4096 {
			return nil, false
		}
		switch wire {
		case 0:
			v, err := readVarint(b, &pos)
			if err != nil {
				return nil, false
			}
			out = append(out, pbField{N: n, Wire: 0, Varint: v})
		case 2:
			l, err := readVarint(b, &pos)
			if err != nil || pos+int(l) > len(b) {
				return nil, false
			}
			out = append(out, pbField{N: n, Wire: 2, Bytes: b[pos : pos+int(l)]})
			pos += int(l)
		case 5:
			if pos+4 > len(b) {
				return nil, false
			}
			out = append(out, pbField{N: n, Wire: 5, Bytes: b[pos : pos+4]})
			pos += 4
		case 1:
			if pos+8 > len(b) {
				return nil, false
			}
			out = append(out, pbField{N: n, Wire: 1, Bytes: b[pos : pos+8]})
			pos += 8
		default:
			return nil, false
		}
	}
	return out, true
}

func f32(b []byte) float32 {
	if len(b) < 4 {
		return 0
	}
	return math.Float32frombits(binary.LittleEndian.Uint32(b))
}

type gNode struct {
	Index  int
	TypeID uint64
	X, Y   float32
	HasPos bool
	Title  string
	Texts  []string
	Pins   [][]byte // 原始引脚描述符（节点条目的 f4）
}

type gGraph struct {
	Name  string
	Nodes []gNode
	Links [][2]int // 节点索引之间的**无向**连线
}

// pinRefs 从一个引脚描述符里取出**候选的节点索引引用**。
//
// 引脚描述符形如：f1 {f1: <节点>, f2: <引脚>}、f2 {f1: <节点>, f2: <引脚>}，
// 有时还有 f3 {…载荷…}、f4 <小整数>。这里**只读直接子节点 f1/f2**，
// 因为更深的消息（f5 等）装的是变量 / 槽位索引，会和节点索引撞在一起。
func pinRefs(pin []byte) []int {
	fields, ok := parseMessage(pin)
	if !ok {
		return nil
	}
	out := []int{}
	for _, f := range fields {
		if (f.N == 1 || f.N == 2) && f.Wire == 2 {
			if inner, ok := parseMessage(f.Bytes); ok {
				for _, g := range inner {
					if g.N == 1 && g.Wire == 0 {
						out = append(out, int(g.Varint))
						break
					}
				}
			}
		}
	}
	return out
}

// collectTexts 走查节点条目的子树，把每个可读字符串连同**它所在的字段路径**一起收集起来，
// 这样才能把"自定义标题"和"变量引用"区分开。
func collectTexts(b []byte, path string, labels map[string]string, out *[]string, depth int) {
	if depth > 12 {
		return
	}
	fields, ok := parseMessage(b)
	if !ok {
		return
	}
	for _, f := range fields {
		p := fmt.Sprintf("%s.f%d", path, f.N)
		switch f.Wire {
		case 2:
			if isTexty(f.Bytes) && len(f.Bytes) > 0 {
				s := string(f.Bytes)
				if labels != nil {
					if cur, seen := labels[p]; !seen || len(s) > len(cur) {
						labels[p] = s
					}
				}
				*out = append(*out, s)
			} else {
				collectTexts(f.Bytes, p, labels, out, depth+1)
			}
		}
	}
}

// graphBody 逐层下钻包装层，直到找到**真正装图**的那条消息：带图名（f2 文本）或
// 节点条目（f3 消息）的那一条。实测文件把每张图多包了一层（f1 -> f1 -> {头部, 图名, 节点}）。
func graphBody(b []byte) []pbField {
	fields, ok := parseMessage(b)
	if !ok {
		return nil
	}
	hasName, hasEntries := false, false
	for _, f := range fields {
		if f.N == 2 && f.Wire == 2 && isTexty(f.Bytes) {
			hasName = true
		}
		if f.N == 3 && f.Wire == 2 {
			if inner, ok := parseMessage(f.Bytes); ok && len(inner) > 0 {
				hasEntries = true
			}
		}
	}
	if hasName || hasEntries {
		return fields
	}
	for _, f := range fields {
		if f.N == 1 && f.Wire == 2 {
			if inner := graphBody(f.Bytes); inner != nil {
				return inner
			}
		}
	}
	return fields
}

// extractGraphs 解码某个容器字段载荷里的全部节点图。
func extractGraphs(payload []byte) []gGraph {
	top, ok := parseMessage(payload)
	if !ok {
		return nil
	}
	graphs := []gGraph{}
	for _, g := range top {
		if g.N != 1 || g.Wire != 2 {
			continue
		}
		body := graphBody(g.Bytes)
		if body == nil {
			continue
		}
		var cur gGraph
		for _, bf := range body {
			switch {
			case bf.N == 2 && bf.Wire == 2 && isTexty(bf.Bytes):
				cur.Name = string(bf.Bytes)
			case bf.N == 3 && bf.Wire == 2:
				entry, ok := parseMessage(bf.Bytes)
				if !ok {
					continue
				}
				var node gNode
				for _, ef := range entry {
					switch {
					case ef.N == 1 && ef.Wire == 0:
						node.Index = int(ef.Varint)
					case ef.N == 2 && ef.Wire == 2:
						if rec, ok := parseMessage(ef.Bytes); ok {
							for _, rf := range rec {
								if rf.N == 5 && rf.Wire == 0 {
									node.TypeID = rf.Varint
								}
							}
						}
					case ef.N == 5 && ef.Wire == 5:
						node.X = f32(ef.Bytes)
						node.HasPos = true
					case ef.N == 6 && ef.Wire == 5:
						node.Y = f32(ef.Bytes)
					case ef.N == 4 && ef.Wire == 2:
						node.Pins = append(node.Pins, ef.Bytes)
					}
				}
				labels := map[string]string{}
				collectTexts(bf.Bytes, "", labels, &node.Texts, 0)
				// 节点自定义标题位于 ...f105.f1
				for path, s := range labels {
					if strings.HasSuffix(path, ".f105.f1") {
						node.Title = s
					}
				}
				cur.Nodes = append(cur.Nodes, node)
			}
		}
		if cur.Name != "" || len(cur.Nodes) > 0 {
			// 解析连线：凡是引用了同一张图里另一个节点的引脚引用
			valid := map[int]bool{}
			for _, n := range cur.Nodes {
				valid[n.Index] = true
			}
			seen := map[[2]int]bool{}
			for _, n := range cur.Nodes {
				for _, pin := range n.Pins {
					for _, ref := range pinRefs(pin) {
						if ref == n.Index || !valid[ref] {
							continue
						}
						a, b := n.Index, ref
						if a > b {
							a, b = b, a
						}
						if seen[[2]int{a, b}] {
							continue
						}
						seen[[2]int{a, b}] = true
						cur.Links = append(cur.Links, [2]int{a, b})
					}
				}
			}
			graphs = append(graphs, cur)
		}
	}
	return graphs
}

// loadNodeTypes 把已验证的内置名字（nodetypes.go）与可选的 node-types.txt 合并。
// 文件不存在不算错误：只用内置表。
func loadNodeTypes(path string) map[uint64]string {
	out := map[uint64]string{}
	for id, name := range builtinNodeTypes {
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
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			parts = strings.SplitN(line, " ", 2)
		}
		if len(parts) != 2 {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil {
			continue
		}
		out[id] = strings.TrimSpace(parts[1])
	}
	return out
}

// renderGraphs 生成纯文本中间文件（全程不用 JSON）。
//
// 刻意保持最小：节点 id + 解析出的名字 + 可选自定义标题 + 引用，然后是连线。
// **位置被略去** —— 一旦改成竖向流程而不是画布，坐标就是噪声。
func renderGraphs(srcName string, graphs []gGraph, types map[uint64]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# node graphs - %s\n\n", srcName)
	fmt.Fprintf(&b, "graphs: %d\n", len(graphs))
	freq := map[uint64]int{}
	for _, g := range graphs {
		fmt.Fprintf(&b, "\n## %s\n\n", g.Name)
		b.WriteString("nodes:\n")
		for _, n := range g.Nodes {
			name := typeLabelFor(n.TypeID, types)
			freq[n.TypeID]++
			line := fmt.Sprintf("  %d\t%s", n.Index, name)
			if n.Title != "" {
				line += "\t\"" + n.Title + "\""
			}
			kept := []string{}
			for _, r := range uniqStrings(n.Texts) {
				if r != n.Title {
					kept = append(kept, r)
				}
			}
			if len(kept) > 0 {
				line += "\trefs: " + strings.Join(kept, ",")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("links:\n")
		b.WriteString("  # EXPERIMENTAL - not verified against a known graph, do not trust yet\n")
		b.WriteString("  # 关卡：引脚块里解出的候选引用，实测 14 条已知边只对上 2 条（见 docs/node-graph-extraction.md 6b/6c）\n")
		b.WriteString("  # 资产：分组 f4 → f5 子树第一个 varint 指向的节点索引，**尚未用截图核对**\n")
		if len(g.Links) == 0 {
			b.WriteString("  (none)\n")
		} else {
			for _, l := range g.Links {
				fmt.Fprintf(&b, "  %d - %d\n", l[0], l[1])
			}
		}
	}
	unknown := []uint64{}
	for id := range freq {
		// 引用值（≥ refTypeBase）与复合节点边界不是"未映射的节点类型"，不进工作清单
		if types[id] == "" && id < refTypeBase && id != assetBoundaryType {
			unknown = append(unknown, id)
		}
	}
	sort.Slice(unknown, func(i, j int) bool { return freq[unknown[i]] > freq[unknown[j]] })
	b.WriteString("## types needing a mapping\n\n")
	if len(unknown) == 0 {
		b.WriteString("none - every type id resolved\n")
	} else {
		b.WriteString("add these to node-types.txt as \"id<TAB>name\":\n\n")
		for _, id := range unknown {
			fmt.Fprintf(&b, "- %d   (used %d×)\n", id, freq[id])
		}
	}
	return b.String()
}

func uniqStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// dumpPins 是调试辅助：把每张图的每个引脚描述符按原始形式打印出来，
// 便于拿"接线已知（来自截图）"的图来标定字段布局。
func dumpPins(payload []byte) string {
	var b strings.Builder
	top, ok := parseMessage(payload)
	if !ok {
		return "payload does not parse\n"
	}
	for _, g := range top {
		if g.N != 1 || g.Wire != 2 {
			continue
		}
		body := graphBody(g.Bytes)
		if body == nil {
			continue
		}
		name := ""
		for _, bf := range body {
			if bf.N == 2 && bf.Wire == 2 && isTexty(bf.Bytes) {
				name = string(bf.Bytes)
			}
		}
		fmt.Fprintf(&b, "\n=== graph %q ===\n", name)
		for _, bf := range body {
			if bf.N != 3 || bf.Wire != 2 {
				continue
			}
			entry, ok := parseMessage(bf.Bytes)
			if !ok {
				continue
			}
			idx, typeID := 0, uint64(0)
			var pins [][]byte
			for _, ef := range entry {
				switch {
				case ef.N == 1 && ef.Wire == 0:
					idx = int(ef.Varint)
				case ef.N == 2 && ef.Wire == 2:
					if rec, ok := parseMessage(ef.Bytes); ok {
						for _, rf := range rec {
							if rf.N == 5 && rf.Wire == 0 {
								typeID = rf.Varint
							}
						}
					}
				case ef.N == 4 && ef.Wire == 2:
					pins = append(pins, ef.Bytes)
				}
			}
			fmt.Fprintf(&b, "  node %d type %d  (%d pins, entry %d bytes)\n", idx, typeID, len(pins), len(bf.Bytes))
			for i, pin := range pins {
				pf, ok := parseMessage(pin)
				if !ok {
					fmt.Fprintf(&b, "    pin%d raw %s\n", i, shortHex(pin, 24))
					continue
				}
				parts := []string{}
				for _, c := range pf {
					if (c.N == 1 || c.N == 2) && c.Wire == 2 {
						if inner, ok := parseMessage(c.Bytes); ok {
							kv := []string{}
							for _, x := range inner {
								if x.Wire == 0 {
									kv = append(kv, fmt.Sprintf("f%d=%d", x.N, x.Varint))
								}
							}
							parts = append(parts, fmt.Sprintf("f%d{%s}", c.N, strings.Join(kv, ",")))
						}
					}
				}
				fmt.Fprintf(&b, "    pin%d len=%d  %s\n", i, len(pin), strings.Join(parts, "  |  "))
			}
		}
	}
	return b.String()
}
