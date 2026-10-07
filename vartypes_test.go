package main

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// encDecl 拼出实测到的声明形状：field1{ field2: 名字, field3: 类型id }。
func encDecl(name string, typeID uint64) []byte {
	inner := encBytesField(2, []byte(name))
	inner = append(inner, encVarintField(3, typeID)...)
	return encBytesField(1, inner)
}

func TestScanVariableDeclarations(t *testing.T) {
	raw := append([]byte("padpadpad!"), encDecl("spawn_unit", 21)...)
	raw = append(raw, encDecl("spawn_interval", 10)...)
	raw = append(raw, encDecl("spawn_unit", 21)...) // 同一个声明出现在另一个元件上

	decls := scanVariableDeclarations(raw)
	if len(decls) != 2 {
		t.Fatalf("expected 2 distinct declarations, got %d (%+v)", len(decls), decls)
	}
	byName := map[string]VarDecl{}
	for _, d := range decls {
		byName[d.Name] = d
	}
	if got := byName["spawn_unit"]; got.TypeID != 21 || got.Sites != 2 {
		t.Errorf("spawn_unit = type %d sites %d, want type 21 sites 2", got.TypeID, got.Sites)
	}
	if got := byName["spawn_interval"]; got.TypeID != 10 {
		t.Errorf("spawn_interval type = %d, want 10", got.TypeID)
	}
}

// 这个特性存在的原因就是那个坑：**元件ID 被存进了数值型变量**。
func TestLintFlagsReferenceNameWithNumericType(t *testing.T) {
	raw := encDecl("spawn_unit", 3) // 3 = 整数
	findings := lintExport(raw, 10)
	if !hasFinding(findings, "reference-typed-as-number", "spawn_unit") {
		t.Fatalf("expected reference-typed-as-number for spawn_unit, got %+v", findings)
	}
}

func TestLintAcceptsReferenceNameWithReferenceType(t *testing.T) {
	raw := encDecl("spawn_unit", 21) // 21 = 元件ID，正确写法
	if hasFinding(lintExport(raw, 10), "reference-typed-as-number", "spawn_unit") {
		t.Error("spawn_unit declared as 元件ID must not be flagged")
	}
}

// 同一个名字在容器里出现两种类型，一定是写错了。
func TestLintFlagsVariableTypeConflict(t *testing.T) {
	raw := append(encDecl("spawn_unit", 21), encDecl("spawn_unit", 3)...)
	if !hasFinding(lintExport(raw, 10), "variable-type-conflict", "spawn_unit") {
		t.Error("expected variable-type-conflict when one name carries two types")
	}
}

// 非浮点类型却带浮点默认值 —— 自相矛盾。
// 注意真实格式里默认值是**长度分隔的 4 字节块**（"0a 04 <float32>"），不是 fixed32 字段。
func TestLintFlagsDefaultTypeMismatch(t *testing.T) {
	blob := make([]byte, 4)
	binary.LittleEndian.PutUint32(blob, math.Float32bits(12.0))
	inner := encVarintField(3, 3) // 声明为整数
	inner = append(inner, encBytesField(1, blob)...)
	raw := append(encDecl("spawn_interval", 3), encBytesField(20, inner)...)

	if !hasFinding(lintExport(raw, 10), "default-value-type-mismatch", "spawn_interval") {
		t.Errorf("expected default-value-type-mismatch, got %+v", lintExport(raw, 10))
	}
}

func TestRenderVarTableListsTypes(t *testing.T) {
	decls := scanVariableDeclarations(encDecl("spawn_unit", 21))
	out := renderVarTable(decls, knownVarTypes)
	for _, want := range []string{"spawn_unit", "元件ID"} {
		if !strings.Contains(out, want) {
			t.Errorf("variable table is missing %q\n%s", want, out)
		}
	}
}
