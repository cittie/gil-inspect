package main

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// encDecl builds the observed declaration shape: field1{ field2: name, field3: typeID }.
func encDecl(name string, typeID uint64) []byte {
	inner := encBytesField(2, []byte(name))
	inner = append(inner, encVarintField(3, typeID)...)
	return encBytesField(1, inner)
}

func TestScanVariableDeclarations(t *testing.T) {
	raw := append([]byte("padpadpad!"), encDecl("spawn_unit", 21)...)
	raw = append(raw, encDecl("spawn_interval", 10)...)
	raw = append(raw, encDecl("spawn_unit", 21)...) // same declaration on another element

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

// The bug this feature exists for: an element id stored in a numeric variable.
func TestLintFlagsReferenceNameWithNumericType(t *testing.T) {
	raw := encDecl("spawn_unit", 3) // 3 = 整数
	findings := lintExport(raw, 10)
	if !hasFinding(findings, "reference-typed-as-number", "spawn_unit") {
		t.Fatalf("expected reference-typed-as-number for spawn_unit, got %+v", findings)
	}
}

func TestLintAcceptsReferenceNameWithReferenceType(t *testing.T) {
	raw := encDecl("spawn_unit", 21) // 21 = 元件ID, correct
	if hasFinding(lintExport(raw, 10), "reference-typed-as-number", "spawn_unit") {
		t.Error("spawn_unit declared as 元件ID must not be flagged")
	}
}

// Same name, two different types anywhere in the container is a definite mistake.
func TestLintFlagsVariableTypeConflict(t *testing.T) {
	raw := append(encDecl("spawn_unit", 21), encDecl("spawn_unit", 3)...)
	if !hasFinding(lintExport(raw, 10), "variable-type-conflict", "spawn_unit") {
		t.Error("expected variable-type-conflict when one name carries two types")
	}
}

// A float default under a non-float declared type is a contradiction.
// Note the default is a length-delimited 4-byte blob in the real format
// ("0a 04 <float32>"), not a fixed32 field.
func TestLintFlagsDefaultTypeMismatch(t *testing.T) {
	blob := make([]byte, 4)
	binary.LittleEndian.PutUint32(blob, math.Float32bits(12.0))
	inner := encVarintField(3, 3) // declared 整数
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
