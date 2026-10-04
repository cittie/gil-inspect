package main

import (
	"strings"
	"testing"
)

// buildLintContainer assembles a minimal container: 20-byte header + one graph field.
func buildLintContainer(graphBody []byte) []byte {
	raw := make([]byte, 20)
	return append(raw, encBytesField(10, graphBody)...)
}

// A variable that graphs reference but nothing declares must be reported. Without this test,
// "no findings on the real file" could just as well mean the check never fires.
func TestLintFindsDanglingReference(t *testing.T) {
	// node entry: index 1, record with type 3360, plus a text field carrying the name
	rec := encVarintField(5, 3360)
	entry := encVarintField(1, 1)
	entry = append(entry, encBytesField(2, rec)...)
	entry = append(entry, encBytesField(105, encBytesField(1, []byte("ghost_variable")))...)
	body := encBytesField(2, []byte("测试图"))
	body = append(body, encBytesField(3, entry)...)
	raw := buildLintContainer(encBytesField(1, body)) // graph wrapper level

	findings := lintExport(raw, 10)
	if !hasFinding(findings, "dangling-reference", "ghost_variable") {
		t.Fatalf("expected a dangling-reference finding for ghost_variable, got %+v", findings)
	}
}

// The same name appearing outside the graph field must clear it.
func TestLintAcceptsDeclaredReference(t *testing.T) {
	rec := encVarintField(5, 3360)
	entry := encVarintField(1, 1)
	entry = append(entry, encBytesField(2, rec)...)
	entry = append(entry, encBytesField(105, encBytesField(1, []byte("declared_var")))...)
	body := encBytesField(2, []byte("测试图"))
	body = append(body, encBytesField(3, entry)...)
	raw := buildLintContainer(encBytesField(1, body))
	// a different top-level field carries the declaration
	raw = append(raw, encBytesField(4, []byte("declared_var"))...)

	if hasFinding(lintExport(raw, 10), "dangling-reference", "declared_var") {
		t.Error("declared_var is declared elsewhere and must not be flagged")
	}
}

// Short alphanumeric runs are internal ids, not names.
func TestLintIgnoresShortCodes(t *testing.T) {
	entry := encVarintField(1, 1)
	entry = append(entry, encBytesField(105, encBytesField(1, []byte("rCB")))...)
	body := encBytesField(2, []byte("图"))
	body = append(body, encBytesField(3, entry)...)
	raw := buildLintContainer(encBytesField(1, body))

	if hasFinding(lintExport(raw, 10), "dangling-reference", "rCB") {
		t.Error("a 3-character code must not be reported as a dangling reference")
	}
}

// A graph name fragment (CJK) must not be reported as a dangling name.
func TestLintIgnoresGraphNameFragments(t *testing.T) {
	entry := encVarintField(1, 1)
	body := encBytesField(2, []byte("关卡-建筑销毁"))
	body = append(body, encBytesField(3, entry)...)
	raw := buildLintContainer(encBytesField(1, body))

	for _, f := range lintExport(raw, 10) {
		if f.Code == "graph-only-name" && strings.Contains(f.What, "建筑销毁") {
			t.Errorf("graph name fragment reported as graph-only-name: %s", f.What)
		}
	}
}

// A stop at the natural end of file is not a parse problem.
func TestLintParseHealthOnlyOnEarlyStop(t *testing.T) {
	entry := encVarintField(1, 1)
	body := encBytesField(2, []byte("图"))
	body = append(body, encBytesField(3, entry)...)
	raw := buildLintContainer(encBytesField(1, body))

	for _, f := range lintExport(raw, 10) {
		if f.Code == "parse-health" {
			t.Errorf("clean container reported a parse-health warning: %s", f.What)
		}
	}
}

func hasFinding(findings []Finding, code, needle string) bool {
	for _, f := range findings {
		if f.Code == code && strings.Contains(f.What, needle) {
			return true
		}
	}
	return false
}
