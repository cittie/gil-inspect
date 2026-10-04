// Graph dump: expand the node-graph section of a .gil into an indented tree.
//
// This is the exploratory half of node-graph extraction. The container stores node graphs
// in one top-level field (observed: f10) as plain protobuf, but the schema is undocumented,
// so we render the tree generically - field numbers, wire types, values, inline strings and
// float32 payloads - and identify structure by reading it.
//
// Output is Markdown (no JSON): one line per field, indented by nesting depth.
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	maxDumpDepth = 32
	maxDumpLines = 400000
)

type dumper struct {
	sb    strings.Builder
	lines int
}

func (d *dumper) line(indent int, format string, a ...any) {
	if d.lines >= maxDumpLines {
		return
	}
	d.lines++
	d.sb.WriteString(strings.Repeat("  ", indent))
	fmt.Fprintf(&d.sb, format+"\n", a...)
}

// looksLikeMessage reports whether b parses cleanly and completely as a protobuf message.
// Used to decide between "nested message" and "opaque bytes / text".
func looksLikeMessage(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	pos, fields := 0, 0
	for pos < len(b) {
		key, err := readVarint(b, &pos)
		if err != nil {
			return false
		}
		n := int(key >> 3)
		wire := int(key & 7)
		if n <= 0 || n > 4096 {
			return false
		}
		switch wire {
		case 0:
			if _, err := readVarint(b, &pos); err != nil {
				return false
			}
		case 2:
			l, err := readVarint(b, &pos)
			if err != nil || pos+int(l) > len(b) {
				return false
			}
			pos += int(l)
		case 5:
			if pos+4 > len(b) {
				return false
			}
			pos += 4
		case 1:
			if pos+8 > len(b) {
				return false
			}
			pos += 8
		default:
			return false
		}
		fields++
	}
	return fields > 0 && pos == len(b)
}

// isTexty reports whether b looks like human-readable UTF-8 rather than binary noise.
func isTexty(b []byte) bool {
	if len(b) == 0 || !utf8.Valid(b) {
		return false
	}
	printable, total := 0, 0
	for _, r := range string(b) {
		total++
		if r == '\n' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			printable++
		}
	}
	return total > 0 && printable*10 >= total*9
}

func shortHex(b []byte, limit int) string {
	if len(b) > limit {
		b = b[:limit]
	}
	parts := make([]string, 0, len(b))
	for _, c := range b {
		parts = append(parts, fmt.Sprintf("%02x", c))
	}
	return strings.Join(parts, " ")
}

func (d *dumper) fields(b []byte, indent, depth int) {
	pos := 0
	for pos < len(b) {
		keyStart := pos
		key, err := readVarint(b, &pos)
		if err != nil {
			d.line(indent, "<stop: %v at offset %d>", err, keyStart)
			return
		}
		n := int(key >> 3)
		wire := int(key & 7)
		if n <= 0 {
			d.line(indent, "<stop: field 0 at offset %d>", keyStart)
			return
		}
		switch wire {
		case 0:
			v, err := readVarint(b, &pos)
			if err != nil {
				d.line(indent, "<stop: %v>", err)
				return
			}
			d.line(indent, "f%d varint %d", n, v)

		case 2:
			l, err := readVarint(b, &pos)
			if err != nil {
				d.line(indent, "<stop: %v>", err)
				return
			}
			if pos+int(l) > len(b) {
				d.line(indent, "f%d len=%d <runs past end>", n, l)
				return
			}
			payload := b[pos : pos+int(l)]
			switch {
			case depth < maxDumpDepth && looksLikeMessage(payload):
				d.line(indent, "f%d message len=%d", n, len(payload))
				d.fields(payload, indent+1, depth+1)
			case isTexty(payload):
				d.line(indent, "f%d text len=%d %q", n, len(payload), string(payload))
			default:
				d.line(indent, "f%d raw len=%d  %s", n, len(payload), shortHex(payload, 24))
			}
			pos += int(l)

		case 5:
			if pos+4 > len(b) {
				d.line(indent, "f%d fixed32 <truncated>", n)
				return
			}
			bits := binary.LittleEndian.Uint32(b[pos : pos+4])
			f := math.Float32frombits(bits)
			d.line(indent, "f%d fixed32 @%d  u=%d  f=%g", n, pos, bits, f)
			pos += 4

		case 1:
			if pos+8 > len(b) {
				d.line(indent, "f%d fixed64 <truncated>", n)
				return
			}
			bits := binary.LittleEndian.Uint64(b[pos : pos+8])
			d.line(indent, "f%d fixed64 @%d  u=%d  d=%g", n, pos, bits, math.Float64frombits(bits))
			pos += 8

		default:
			d.line(indent, "f%d wire=%d <unsupported, stop at offset %d>", n, wire, keyStart)
			return
		}
	}
}

// dumpField expands one top-level container field into a Markdown tree.
func dumpField(raw []byte, fieldNum int, srcName string) (string, bool) {
	fields, _ := parseFields(raw, 20)
	for _, f := range fields {
		if f.N != fieldNum || f.Wire != "bytes" {
			continue
		}
		// re-walk to recover the payload offset (parseFields does not keep it)
		pos := 20
		for pos < len(raw) {
			key, err := readVarint(raw, &pos)
			if err != nil {
				break
			}
			n := int(key >> 3)
			wire := int(key & 7)
			if wire == 0 {
				if _, err := readVarint(raw, &pos); err != nil {
					break
				}
				continue
			}
			if wire != 2 {
				break
			}
			l, err := readVarint(raw, &pos)
			if err != nil {
				break
			}
			if n == fieldNum {
				payload := raw[pos : pos+int(l)]
				d := &dumper{}
				d.line(0, "# %s - field %d tree", srcName, fieldNum)
				d.line(0, "")
				d.line(0, "payload %d bytes at offset %d", len(payload), pos)
				d.line(0, "")
				d.fields(payload, 0, 0)
				if d.lines >= maxDumpLines {
					d.line(0, "")
					d.line(0, "<truncated at %d lines>", maxDumpLines)
				}
				return d.sb.String(), true
			}
			pos += int(l)
		}
	}
	return "", false
}

// fieldPayload returns the raw bytes of one top-level container field (wire type 2).
func fieldPayload(raw []byte, fieldNum int) ([]byte, bool) {
	pos := 20
	for pos < len(raw) {
		key, err := readVarint(raw, &pos)
		if err != nil {
			return nil, false
		}
		n := int(key >> 3)
		wire := int(key & 7)
		if wire == 0 {
			if _, err := readVarint(raw, &pos); err != nil {
				return nil, false
			}
			continue
		}
		if wire != 2 {
			return nil, false
		}
		l, err := readVarint(raw, &pos)
		if err != nil || pos+int(l) > len(raw) {
			return nil, false
		}
		if n == fieldNum {
			return raw[pos : pos+int(l)], true
		}
		pos += int(l)
	}
	return nil, false
}
