// 资产文件（.gia）里复合节点图的解码。
//
// 与关卡存档（.gil）的差别（实测）：
//
//	关卡：一张图一个顶层 f1，节点条目自带唯一索引与坐标
//	资产：一条**定义**记录（含名字）+ 一条**实现**记录（含图体），二者按**顺序**配对
//	      （那两处小 id 是"种类标记"，不是身份：定义恒为 f1.f2=23 / f2.f2=5，实现恒为 f1.f2=5）
//
// 实现体的结构：
//
//	f13 -> f1 -> f1                 图体
//	  图体 f1                        头部（复合节点边界伪节点，类型 0x40000001）
//	  图体 f3 = 一个"节点分组"
//	    分组 f1 (varint)             节点索引
//	    分组 f2 / f3                 同一个节点的两份记录，其 f5 = **节点类型 id**
//	    分组 f4 (可多个)              引脚描述符；其 f5 子树里的第一个 varint 疑似**被连接节点的索引**
//	    分组 f5 / f6 (fixed32)       **坐标 x / y**（与关卡同布局）
//
// ⚠️ 有些节点的"类型槽位"装的是**引用值**而不是类型 id（例如 0x40000000+23、0x60000000+99），
// 所以读取时把 ≥ refTypeBase 的值当作引用，不当作节点类型。
package main

const (
	// assetBoundaryType：复合节点自己的入口/出口伪节点用的类型标记。
	assetBoundaryType = 0x40000001
	// refTypeBase：≥ 该值的"类型"其实是资源/配置引用，不是节点类型。
	refTypeBase = 0x10000000
	// assetRecordDefinition / assetRecordImplementation：记录级 f5 的种类标记。
	assetRecordDefinition     = 12
	assetRecordImplementation = 9
)

// parseFieldsTolerant 与 parseMessage 类似，但**遇到尾部垃圾时保留已解析的字段**。
// 实测导出文件末尾会有几个字节不是合法字段（例如 160018 偏移处），
// 而 parseMessage 要求"完整、干净地"解析，这种情况下会整体失败、一个字段都拿不到。
func parseFieldsTolerant(b []byte) ([]pbField, bool) {
	out := []pbField{}
	pos := 0
	for pos < len(b) {
		key, err := readVarint(b, &pos)
		if err != nil {
			return out, len(out) > 0
		}
		n := int(key >> 3)
		wire := int(key & 7)
		if n <= 0 || n > 65535 {
			return out, len(out) > 0
		}
		switch wire {
		case 0:
			v, err := readVarint(b, &pos)
			if err != nil {
				return out, len(out) > 0
			}
			out = append(out, pbField{N: n, Wire: 0, Varint: v})
		case 2:
			l, err := readVarint(b, &pos)
			if err != nil || pos+int(l) > len(b) {
				return out, len(out) > 0
			}
			out = append(out, pbField{N: n, Wire: 2, Bytes: b[pos : pos+int(l)]})
			pos += int(l)
		case 5:
			if pos+4 > len(b) {
				return out, len(out) > 0
			}
			out = append(out, pbField{N: n, Wire: 5, Bytes: b[pos : pos+4]})
			pos += 4
		case 1:
			if pos+8 > len(b) {
				return out, len(out) > 0
			}
			out = append(out, pbField{N: n, Wire: 1, Bytes: b[pos : pos+8]})
			pos += 8
		default:
			return out, len(out) > 0
		}
	}
	return out, len(out) > 0
}

// headerWord 取容器头第 idx 个（0 起）大端 32 位字。
func headerWord(raw []byte, idx int) uint32 {
	off := idx * 4
	if off+3 >= len(raw) {
		return 0
	}
	return uint32(raw[off])<<24 | uint32(raw[off+1])<<16 | uint32(raw[off+2])<<8 | uint32(raw[off+3])
}

// isAssetContainer 判断是不是资产文件：容器头**第 4 个字**（偏移 12）是"容器种类"
// （2 = 关卡存档，3 = 资产）。⚠️ 第 3 个字（偏移 8）是常量 806，**不是**种类，别搞混。
func isAssetContainer(raw []byte) bool {
	return headerWord(raw, 3) == 3
}

// assetGraphBody 从一条实现记录里取出图体（实测路径 f13 -> f1 -> f1）。
func assetGraphBody(impl []byte) []byte {
	fields, ok := parseFieldsTolerant(impl)
	if !ok {
		return nil
	}
	for _, f := range fields {
		if f.N != 13 || f.Wire != 2 {
			continue
		}
		body := f.Bytes
		for depth := 0; depth < 2; depth++ {
			inner, ok := parseFieldsTolerant(body)
			if !ok {
				return body
			}
			next := []byte(nil)
			for _, g := range inner {
				if g.N == 1 && g.Wire == 2 {
					next = g.Bytes
					break
				}
			}
			if next == nil {
				break
			}
			body = next
		}
		return body
	}
	return nil
}

// firstVarintDeep 在子树里找第一个 varint（用于从引脚描述符里取候选目标节点索引）。
func firstVarintDeep(b []byte, depth int) (uint64, bool) {
	if depth > 3 {
		return 0, false
	}
	fields, ok := parseFieldsTolerant(b)
	if !ok {
		return 0, false
	}
	for _, f := range fields {
		if f.Wire == 0 {
			return f.Varint, true
		}
		if f.Wire == 2 {
			if v, ok := firstVarintDeep(f.Bytes, depth+1); ok {
				return v, true
			}
		}
	}
	return 0, false
}

// extractAssetGraphs 解码一个资产文件里全部复合节点的图（定义与实现按顺序配对）。
func extractAssetGraphs(raw []byte) []gGraph {
	if len(raw) <= 20 {
		return nil
	}
	top, ok := parseFieldsTolerant(raw[20:])
	if !ok {
		return nil
	}

	var names []string
	var impls [][]byte
	for _, f := range top {
		if f.Wire != 2 {
			continue
		}
		inner, ok := parseFieldsTolerant(f.Bytes)
		if !ok {
			continue
		}
		name, kind := "", uint64(0)
		for _, g := range inner {
			if g.N == 3 && g.Wire == 2 && isTexty(g.Bytes) {
				name = string(g.Bytes)
			}
			if g.N == 5 && g.Wire == 0 {
				kind = g.Varint
			}
		}
		switch {
		case kind == assetRecordDefinition && name != "":
			names = append(names, name)
		case kind == assetRecordImplementation:
			impls = append(impls, f.Bytes)
		}
	}

	graphs := []gGraph{}
	for i, name := range names {
		if i >= len(impls) {
			break
		}
		body := assetGraphBody(impls[i])
		if body == nil {
			continue
		}
		cur := readAssetGraph(name, body)
		if len(cur.Nodes) > 0 {
			graphs = append(graphs, cur)
		}
	}
	return graphs
}

// readAssetGraph 解析图体里的"节点分组"，并抽取候选连线。
func readAssetGraph(name string, body []byte) gGraph {
	cur := gGraph{Name: name}
	groups, ok := parseFieldsTolerant(body)
	if !ok {
		return cur
	}
	type pending struct {
		index   int
		targets []int
	}
	pendings := []pending{}

	for _, g := range groups {
		if g.N != 3 || g.Wire != 2 {
			continue
		}
		entry, ok := parseFieldsTolerant(g.Bytes)
		if !ok {
			continue
		}
		var node gNode
		targets := []int{}
		for _, ef := range entry {
			switch {
			case ef.N == 1 && ef.Wire == 0:
				node.Index = int(ef.Varint)
			case ef.N == 2 && ef.Wire == 2:
				if rec, ok := parseFieldsTolerant(ef.Bytes); ok {
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
				for _, pf := range mustFields(ef.Bytes) {
					if pf.N == 5 && pf.Wire == 2 {
						if v, ok := firstVarintDeep(pf.Bytes, 0); ok {
							targets = append(targets, int(v))
						}
					}
				}
			}
		}
		node.Texts = textsOf(g.Bytes)
		pendings = append(pendings, pending{index: node.Index, targets: targets})
		cur.Nodes = append(cur.Nodes, node)
	}

	valid := map[int]bool{}
	for _, n := range cur.Nodes {
		valid[n.Index] = true
	}
	seen := map[[2]int]bool{}
	for _, p := range pendings {
		for _, t := range p.targets {
			if t == p.index || !valid[t] {
				continue
			}
			a, b := p.index, t
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
	return cur
}

func mustFields(b []byte) []pbField {
	out, ok := parseFieldsTolerant(b)
	if !ok {
		return nil
	}
	return out
}

// textsOf 收集一个子树里的可读字符串（引脚名、作者注释）。
func textsOf(b []byte) []string {
	fields, ok := parseFieldsTolerant(b)
	if !ok {
		return nil
	}
	out := []string{}
	for _, f := range fields {
		if f.Wire != 2 {
			continue
		}
		if isTexty(f.Bytes) && len(f.Bytes) > 0 {
			out = append(out, string(f.Bytes))
			continue
		}
		out = append(out, textsOf(f.Bytes)...)
	}
	return out
}

// typeLabelFor 渲染一个类型 id：已知显示名字，未知按大小区分"引用"与"未映射类型"。
func typeLabelFor(id uint64, table map[uint64]string) string {
	if name, ok := table[id]; ok {
		return name
	}
	switch {
	case id == assetBoundaryType:
		return "复合节点边界"
	case id >= refTypeBase:
		return "引用(0x" + upperHex(id) + ")"
	default:
		return "?" + itoa(id)
	}
}

func upperHex(v uint64) string {
	const digits = "0123456789ABCDEF"
	if v == 0 {
		return "0"
	}
	out := []byte{}
	for v > 0 {
		out = append([]byte{digits[v&0xf]}, out...)
		v >>= 4
	}
	return string(out)
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	out := []byte{}
	for v > 0 {
		out = append([]byte{byte('0' + v%10)}, out...)
		v /= 10
	}
	return string(out)
}

// graphsFrom 统一的取图入口：资产文件走复合节点解码，关卡存档走 f10 图字段。
func graphsFrom(raw []byte, graphField int) ([]gGraph, bool) {
	if isAssetContainer(raw) {
		return extractAssetGraphs(raw), true
	}
	payload, ok := fieldPayload(raw, graphField)
	if !ok {
		return nil, false
	}
	return extractGraphs(payload), true
}
