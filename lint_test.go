package main

import (
	"strings"
	"testing"
)

// buildLintContainer 拼一个最小容器：20 字节头 + 一个图字段。
func buildLintContainer(graphBody []byte) []byte {
	raw := make([]byte, 20)
	return append(raw, encBytesField(10, graphBody)...)
}

// 图里引用了、但没有任何地方声明的变量**必须**被报出来。没有这个测试，
// "真实文件上零命中"也可能只是**这条检查从来没生效过**。
func TestLintFindsDanglingReference(t *testing.T) {
	// 节点条目：索引 1，记录类型 3360，外加一个携带名字的文本字段
	rec := encVarintField(5, 3360)
	entry := encVarintField(1, 1)
	entry = append(entry, encBytesField(2, rec)...)
	entry = append(entry, encBytesField(105, encBytesField(1, []byte("ghost_variable")))...)
	body := encBytesField(2, []byte("测试图"))
	body = append(body, encBytesField(3, entry)...)
	raw := buildLintContainer(encBytesField(1, body)) // 图的包装层

	findings := lintExport(raw, 10)
	if !hasFinding(findings, "dangling-reference", "ghost_variable") {
		t.Fatalf("expected a dangling-reference finding for ghost_variable, got %+v", findings)
	}
}

// 同一个名字出现在图字段**之外**时，就不该再报。
func TestLintAcceptsDeclaredReference(t *testing.T) {
	rec := encVarintField(5, 3360)
	entry := encVarintField(1, 1)
	entry = append(entry, encBytesField(2, rec)...)
	entry = append(entry, encBytesField(105, encBytesField(1, []byte("declared_var")))...)
	body := encBytesField(2, []byte("测试图"))
	body = append(body, encBytesField(3, entry)...)
	raw := buildLintContainer(encBytesField(1, body))
	// 由另一个顶层字段承载这个声明
	raw = append(raw, encBytesField(4, []byte("declared_var"))...)

	if hasFinding(lintExport(raw, 10), "dangling-reference", "declared_var") {
		t.Error("declared_var is declared elsewhere and must not be flagged")
	}
}

// 短的字母数字串是内部 id，不是名字。
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

// 图名的片段不能被报成"只在图区出现的名字"。
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

// 停在文件的自然结尾不算解析问题。
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

// 平台只在关卡实体的图上触发 实体销毁时，而我们读不出图属于哪个实体 ——
// 所以只要出现该节点，至少必须**提一句**。
func TestLintMentionsDestroyEventPlacement(t *testing.T) {
	rec := encVarintField(5, 373) // 373 = 实体销毁时（已验证）
	entry := encVarintField(1, 1)
	entry = append(entry, encBytesField(2, rec)...)
	body := encBytesField(2, []byte("元件上的销毁图"))
	body = append(body, encBytesField(3, entry)...)
	raw := buildLintContainer(encBytesField(1, body))

	if !hasFinding(lintExport(raw, 10), "destroy-event-placement", "实体销毁时") {
		t.Errorf("expected a destroy-event-placement reminder, got %+v", lintExport(raw, 10))
	}
}

// 不含销毁事件的图，不能触发这条提醒。
func TestLintDestroyReminderOnlyWhenNodePresent(t *testing.T) {
	rec := encVarintField(5, 2) // 双分支
	entry := encVarintField(1, 1)
	entry = append(entry, encBytesField(2, rec)...)
	body := encBytesField(2, []byte("普通图"))
	body = append(body, encBytesField(3, entry)...)
	raw := buildLintContainer(encBytesField(1, body))

	if hasFinding(lintExport(raw, 10), "destroy-event-placement", "") {
		t.Error("no destroy event node is present, so the reminder must not fire")
	}
}
