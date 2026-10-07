package main

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// --- 极简 protobuf 编码器，让测试不必依赖真实 .gil 文件 ---

func encVarint(v uint64) []byte {
	out := []byte{}
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			out = append(out, b|0x80)
			continue
		}
		return append(out, b)
	}
}

func encVarintField(field int, v uint64) []byte {
	return append(encVarint(uint64(field<<3|0)), encVarint(v)...)
}

func encBytesField(field int, payload []byte) []byte {
	out := append(encVarint(uint64(field<<3|2)), encVarint(uint64(len(payload)))...)
	return append(out, payload...)
}

func encFloatField(field int, f float32) []byte {
	out := append(encVarint(uint64(field<<3|5)), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[len(out)-4:], math.Float32bits(f))
	return out
}

func TestReadVarint(t *testing.T) {
	cases := []struct {
		in   []byte
		want uint64
	}{
		{[]byte{0x00}, 0},
		{[]byte{0x08}, 8},
		{[]byte{0xac, 0x02}, 300},
		{[]byte{0x82, 0x80, 0x80, 0x80, 0x04}, 1073741826},
	}
	for _, c := range cases {
		pos := 0
		got, err := readVarint(c.in, &pos)
		if err != nil {
			t.Fatalf("readVarint(%v): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("readVarint(%v) = %d, want %d", c.in, got, c.want)
		}
		if pos != len(c.in) {
			t.Errorf("readVarint(%v) consumed %d of %d bytes", c.in, pos, len(c.in))
		}
	}
}

func TestReadVarintRejectsTruncated(t *testing.T) {
	pos := 0
	if _, err := readVarint([]byte{0x80}, &pos); err == nil {
		t.Error("expected an error for a truncated varint")
	}
}

func TestParseMessage(t *testing.T) {
	msg := append(encVarintField(1, 42), encBytesField(2, []byte("hi"))...)
	fields, ok := parseMessage(msg)
	if !ok {
		t.Fatal("parseMessage rejected a well-formed message")
	}
	if len(fields) != 2 || fields[0].Varint != 42 || string(fields[1].Bytes) != "hi" {
		t.Fatalf("unexpected fields: %+v", fields)
	}
}

func TestParseMessageRejectsGarbage(t *testing.T) {
	// 长度前缀越界的消息不能被接受
	if _, ok := parseMessage([]byte{0x12, 0x7f, 0x01}); ok {
		t.Error("parseMessage accepted a message whose payload runs past the end")
	}
}

func TestIsTexty(t *testing.T) {
	if !isTexty([]byte("关卡-建筑销毁")) {
		t.Error("isTexty rejected a CJK string")
	}
	if isTexty([]byte{0x00, 0x01, 0x02, 0x03}) {
		t.Error("isTexty accepted control bytes")
	}
}

// buildGraphPayload 复刻实测到的容器布局：
// f1 包装 -> f1 图体 -> {f1 头部, f2 图名, f3 节点条目}
func buildGraphPayload(name string, index int, typeID uint64, x, y float32) []byte {
	header := append(encVarintField(1, 10000), encVarintField(2, 20000)...)
	rec := append(encVarintField(1, 10001), encVarintField(2, 20000)...)
	rec = append(rec, encVarintField(3, 22000)...)
	rec = append(rec, encVarintField(5, typeID)...)
	entry := encVarintField(1, uint64(index))
	entry = append(entry, encBytesField(2, rec)...)
	entry = append(entry, encBytesField(3, rec)...)
	entry = append(entry, encFloatField(5, x)...)
	entry = append(entry, encFloatField(6, y)...)
	body := encBytesField(1, header)
	body = append(body, encBytesField(2, []byte(name))...)
	body = append(body, encBytesField(3, entry)...)
	return encBytesField(1, body)
}

func TestExtractGraphs(t *testing.T) {
	payload := buildGraphPayload("测试图", 7, 3360, -215, -395)
	graphs := extractGraphs(payload)
	if len(graphs) != 1 {
		t.Fatalf("expected 1 graph, got %d", len(graphs))
	}
	g := graphs[0]
	if g.Name != "测试图" {
		t.Errorf("graph name = %q, want 测试图", g.Name)
	}
	if len(g.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(g.Nodes))
	}
	n := g.Nodes[0]
	if n.Index != 7 || n.TypeID != 3360 {
		t.Errorf("node = index %d type %d, want 7 / 3360", n.Index, n.TypeID)
	}
	if n.X != -215 || n.Y != -395 {
		t.Errorf("node position = (%v, %v), want (-215, -395)", n.X, n.Y)
	}
}

func TestRenderGraphsMarksLinksExperimental(t *testing.T) {
	graphs := extractGraphs(buildGraphPayload("G", 1, 2, 0, 0))
	types := map[uint64]string{2: "双分支"}
	out := renderGraphs("x.gil", graphs, types, false)
	for _, want := range []string{"## G", "双分支", "EXPERIMENTAL"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q\n%s", want, out)
		}
	}
}
