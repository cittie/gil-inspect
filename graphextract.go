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
	Pins   [][]byte // raw pin descriptors (f4 of the node entry)
}

type gGraph struct {
	Name  string
	Nodes []gNode
	Links [][2]int // undirected connections between node indices
}

// pinRefs pulls candidate node-index references out of one pin descriptor.
//
// A pin descriptor looks like: f1 {f1: <node>, f2: <pin>}, f2 {f1: <node>, f2: <pin>},
// sometimes f3 {…payload…}, f4 <small int>. Only the direct f1/f2 children are read, because
// deeper messages (f5 etc.) carry variable/slot indices that would collide with node indices.
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
					case ef.N == 4 && ef.Wire == 2:
						node.Pins = append(node.Pins, ef.Bytes)
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
			// resolve links: any pin reference that names another node in the same graph
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

// renderGraphs produces the plain-text intermediate file (no JSON anywhere).
//
// Deliberately minimal: node id + resolved name + optional custom title + refs, then links.
// Positions are omitted - they are noise once you render a vertical flow instead of a canvas.
func renderGraphs(srcName string, graphs []gGraph, types map[uint64]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# node graphs - %s\n\n", srcName)
	fmt.Fprintf(&b, "graphs: %d\n", len(graphs))
	freq := map[uint64]int{}
	for _, g := range graphs {
		fmt.Fprintf(&b, "\n## %s\n\n", g.Name)
		b.WriteString("nodes:\n")
		for _, n := range g.Nodes {
			name := types[n.TypeID]
			if name == "" {
				name = fmt.Sprintf("?%d", n.TypeID)
			}
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
		b.WriteString("  # the pin blocks decoded so far do not contain the real node pairs; see\n")
		b.WriteString("  # docs/node-graph-extraction.md 6b/6c for the ground truth this must reproduce\n")
		if len(g.Links) == 0 {
			b.WriteString("  (none)\n")
		} else {
			for _, l := range g.Links {
				fmt.Fprintf(&b, "  %d - %d\n", l[0], l[1])
			}
		}
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

// dumpPins is a debugging aid: it prints every pin descriptor of every graph in raw form, so
// the field layout can be calibrated against a graph whose wiring is known from a screenshot.
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
