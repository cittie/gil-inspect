package main

import (
	"testing"
)

// 拼一个资产（.gia）容器：20 字节头（第 4 个字 = 3 表示资产）+ 顶层记录。
func buildAssetContainer(records ...[]byte) []byte {
	raw := make([]byte, 20)
	raw[11] = 6 // 头第 3 个字是常量 806 的低字节（实测），不是种类
	raw[15] = 3 // 头第 4 个字（偏移 12..15）大端值 = 3 → 资产
	for _, r := range records {
		raw = append(raw, encBytesField(1, r)...)
	}
	return raw
}

// 一条定义记录（种类标记 12，带名字）。
func assetDefinition(name string) []byte {
	out := encBytesField(3, []byte(name))
	return append(out, encVarintField(5, assetRecordDefinition)...)
}

// 一条实现记录（种类标记 9），内部是 f13 -> f1 -> f1 的图体。
func assetImplementation(groups ...[]byte) []byte {
	body := []byte{}
	for _, g := range groups {
		body = append(body, encBytesField(3, g)...)
	}
	inner := encBytesField(1, encBytesField(1, body))
	out := encBytesField(13, inner)
	return append(out, encVarintField(5, assetRecordImplementation)...)
}

// 一个"节点分组"：f1 索引，f2 记录（f5 = 类型），f5/f6 坐标。
func assetGroup(index int, typeID uint64, x, y float32, pins ...[]byte) []byte {
	rec := encVarintField(5, typeID)
	out := encVarintField(1, uint64(index))
	out = append(out, encBytesField(2, rec)...)
	out = append(out, encBytesField(3, rec)...)
	for _, p := range pins {
		out = append(out, encBytesField(4, p)...)
	}
	out = append(out, encFloatField(5, x)...)
	return append(out, encFloatField(6, y)...)
}

func TestIsAssetContainer(t *testing.T) {
	if !isAssetContainer(buildAssetContainer()) {
		t.Error("头第 4 个字为 3 时应判定为资产文件")
	}
	level := make([]byte, 20)
	level[15] = 2 // 头第 4 个字 = 2 → 关卡存档
	if isAssetContainer(level) {
		t.Error("头第 4 个字为 2 时应判定为关卡存档")
	}
}

func TestExtractAssetGraphsPairsDefinitionWithImplementation(t *testing.T) {
	raw := buildAssetContainer(
		assetDefinition("[随机]测试复合节点"),
		assetImplementation(
			assetGroup(1, 99, 10, 20),
			assetGroup(2, 252, 30, 40),
		),
	)
	graphs := extractAssetGraphs(raw)
	if len(graphs) != 1 {
		t.Fatalf("期望 1 张图，得到 %d", len(graphs))
	}
	g := graphs[0]
	if g.Name != "[随机]测试复合节点" {
		t.Errorf("图名 = %q", g.Name)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("期望 2 个节点，得到 %d", len(g.Nodes))
	}
	if g.Nodes[0].TypeID != 99 || g.Nodes[1].TypeID != 252 {
		t.Errorf("类型 id 解析错误：%d / %d", g.Nodes[0].TypeID, g.Nodes[1].TypeID)
	}
	if !g.Nodes[0].HasPos || g.Nodes[0].X != 10 || g.Nodes[0].Y != 20 {
		t.Errorf("坐标解析错误：(%v, %v) hasPos=%v", g.Nodes[0].X, g.Nodes[0].Y, g.Nodes[0].HasPos)
	}
}

// 引脚描述符里的 f5 子树第一个 varint 指向另一个节点 → 作为候选连线。
func TestAssetGraphCandidateLinks(t *testing.T) {
	pin := encBytesField(5, encVarintField(1, 2)) // f5{ f1: 2 } → 指向节点 2
	raw := buildAssetContainer(
		assetDefinition("连线测试"),
		assetImplementation(
			assetGroup(1, 99, 0, 0, pin),
			assetGroup(2, 252, 0, 0),
		),
	)
	g := extractAssetGraphs(raw)[0]
	if len(g.Links) != 1 || g.Links[0] != [2]int{1, 2} {
		t.Errorf("期望候选连线 1-2，得到 %v", g.Links)
	}
}

// 引用值（≥ refTypeBase）与复合节点边界不应被当成"未映射的节点类型"。
func TestTypeLabelForSpecialIDs(t *testing.T) {
	table := map[uint64]string{252: "创建元件"}
	cases := map[uint64]string{
		252:               "创建元件",
		assetBoundaryType: "复合节点边界",
		refTypeBase + 23:  "引用(0x10000017)",
		204:               "?204",
	}
	for id, want := range cases {
		if got := typeLabelFor(id, table); got != want {
			t.Errorf("typeLabelFor(%d) = %q，期望 %q", id, got, want)
		}
	}
}

// 资产走资产解码，关卡走 f10；graphsFrom 是统一入口。
func TestGraphsFromDispatches(t *testing.T) {
	asset := buildAssetContainer(
		assetDefinition("A"),
		assetImplementation(assetGroup(1, 99, 0, 0)),
	)
	if gs, ok := graphsFrom(asset, 10); !ok || len(gs) != 1 {
		t.Errorf("资产应解出 1 张图，得到 %d (ok=%v)", len(gs), ok)
	}

	level := buildLintContainer(encBytesField(1, encBytesField(2, []byte("关卡图"))))
	if gs, ok := graphsFrom(level, 10); !ok || len(gs) != 1 {
		t.Errorf("关卡应解出 1 张图，得到 %d (ok=%v)", len(gs), ok)
	}
}
