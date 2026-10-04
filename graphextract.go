// Graph extraction: turn the node-graph field into a plain-text/markdown intermediate file.
//
// Reverse-engineered shape of one graph (field 10 of the container, one top-level f1 per graph):
//
//	f1  message            graph
//	  f1  message          graph body
//	    f1 message         header   (f1 id, f2 container id, f3 ?, f5 version-ish 0x40000005)
//	    f2 text            graph name
//	    f3 message         one NODE ENTRY, repeated
//	      f1 varint          node index (1, 2, 3, ... consecutive)
//	      f2 message         node record  -> its f1/f2/f3 = ids, f5 = NODE TYPE ID
//	      f3 message         duplicate of the node record
//	      f4 message         pin / connection descriptors (references small indices)
//	      f5 fixed32         position X   (float32)
//	      f6 fixed32         position Y   (float32)
//	      (nested) f105.f1 text   optional custom node title
//
// Node type ids are internal (82, 250, 3360, ...) and the names are NOT in the file, so an
// external lookup table is read from node-types.txt (one "id<TAB>name" per line). Anything
// unmapped is reported as "type N" and listed at the end so the table can grow.
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

// parseMessage decodes a protobuf message into its fields. Returns false if b does not parse
// cleanly and completely (which is how we tell messages apart from opaque payloads/text).
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
}

type gGraph struct {
	Name  string
	Nodes []gNode
}

// collectTexts walks a node entry's subtree and gathers every readable string together with
// the field path that carried it, so we can tell custom titles from variable references.
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

// graphBody descends through wrapper levels until it reaches the message that actually holds
// the graph: the one carrying the name (f2 text) or node entries (f3 messages). Observed files
// wrap each graph one level deep (f1 -> f1 -> {header, name, nodes}).
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

// extractGraphs decodes the graphs in one container field payload.
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
					}
				}
				labels := map[string]string{}
				collectTexts(bf.Bytes, "", labels, &node.Texts, 0)
				// a custom node title lives under ...f105.f1
				for path, s := range labels {
					if strings.HasSuffix(path, ".f105.f1") {
						node.Title = s
					}
				}
				cur.Nodes = append(cur.Nodes, node)
			}
		}
		if cur.Name != "" || len(cur.Nodes) > 0 {
			graphs = append(graphs, cur)
		}
	}
	return graphs
}

// loadNodeTypes reads "id<TAB>name" lines. Missing file is not an error: names simply stay unknown.
func loadNodeTypes(path string) map[uint64]string {
	out := map[uint64]string{}
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

// renderGraphs produces the Markdown intermediate file (no JSON anywhere).
func renderGraphs(srcName string, graphs []gGraph, types map[uint64]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# node graphs - %s\n\n", srcName)
	fmt.Fprintf(&b, "graphs: %d\n\n", len(graphs))
	freq := map[uint64]int{}
	for _, g := range graphs {
		fmt.Fprintf(&b, "## %s\n\n", g.Name)
		fmt.Fprintf(&b, "nodes: %d\n\n", len(g.Nodes))
		b.WriteString("| # | type | name | pos | refs |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")
		for _, n := range g.Nodes {
			name := types[n.TypeID]
			if name == "" {
				name = fmt.Sprintf("? type %d", n.TypeID)
			}
			freq[n.TypeID]++
			pos := ""
			if n.HasPos {
				pos = fmt.Sprintf("(%.4g, %.4g)", n.X, n.Y)
			}
			refs := ""
			if len(n.Texts) > 0 {
				refs = strings.Join(uniqStrings(n.Texts), ", ")
			}
			title := ""
			if n.Title != "" {
				title = "**" + n.Title + "** "
			}
			fmt.Fprintf(&b, "| %d | %d | %s%s | %s | %s |\n", n.Index, n.TypeID, title, name, pos, refs)
		}
		b.WriteString("\n")
	}
	unknown := []uint64{}
	for id, c := range freq {
		if types[id] == "" {
			unknown = append(unknown, id)
		}
		_ = c
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
