// Routine sanity checks over one export ("lint").
//
// Scope is deliberately limited to what this tool can actually see, and every finding is
// phrased as something to verify rather than a verdict -- the .gil does not carry enough
// context to be certain (an identifier that appears only inside graphs may legitimately be a
// graph name, a node's custom title, or a timer name).
//
// The load-bearing signal is simple and measurable: for each identifier that appears in the
// node-graph section, how often does it also appear *elsewhere* in the file (element/entity/
// level definitions)? A variable that graphs use but nothing declares shows up as
// "graphs > 0, elsewhere == 0" -- exactly the dangling-reference case.
package main

import (
	"fmt"
	"sort"
	"strings"
)

// Finding is one lint result. Level is "error", "warn" or "info".
type Finding struct {
	Level string
	Code  string
	What  string
}

// identifiers that are structural noise rather than user-chosen names
var lintNoisePrefixes = []string{
	"GI_", "PRIVATE_", "Beyd_", "Fx_", "MPAction", "Root", "root", "FocusAnchor",
	"MoveHead", "Foot", "Hand", "Weapon", "Chest", "Head", "Neck", "Body", "Waist", "Knee",
	"Billboard", "RUNE", "Bone", "Attach",
}

// names our own design convention forbids (they read as "one side is the player")
var lintBannedNames = []string{"self", "enemy", "my_", "our_", "ally_", "opponent"}

func isNoise(id string) bool {
	for _, p := range lintNoisePrefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

func countOccurrences(haystack, needle string) int {
	if needle == "" {
		return 0
	}
	return strings.Count(haystack, needle)
}

// lintExport runs the checks. graphField is the container field holding the node graphs.
func lintExport(raw []byte, graphField int) []Finding {
	findings := []Finding{}
	add := func(level, code, what string) {
		findings = append(findings, Finding{Level: level, Code: code, What: what})
	}

	// A missing graph field must not abort the checks that do not depend on graphs
	// (variable declarations are checked even in a level that has no graphs at all).
	graphPayload, hasGraphs := fieldPayload(raw, graphField)
	if !hasGraphs {
		add("error", "parse-graph-field", fmt.Sprintf("容器字段 f%d 找不到或不是长度分隔字段：无法检查节点图（变量检查仍会执行）", graphField))
	}
	if _, walkErr := parseFields(raw, 20); walkErr != "" {
		// Observed exports end with a few bytes that are not a valid field (e.g. "stopped at
		// offset 85264" on an 85267-byte file). Only treat an early stop as suspicious.
		if stop := trailingOffset(walkErr); stop < 0 || stop < len(raw)-16 {
			add("warn", "parse-health", "顶层字段走查提前结束："+walkErr+"（文件可能被截断，或格式已变）")
		}
	}

	whole := string(raw)
	graphText := ""
	graphs := []gGraph{}
	if hasGraphs {
		graphText = string(graphPayload)
		graphs = extractGraphs(graphPayload)
	}

	// --- candidate identifiers that graphs reference ---
	graphIDs := uniqSorted(matches(idRe, graphText))
	graphCJK := uniqSorted(matches(cjkRe, graphText))

	declaredGraphNames := map[string]bool{}
	for _, g := range graphs {
		if g.Name != "" {
			declaredGraphNames[g.Name] = true
		}
	}

	// ASCII identifiers: used in graphs but nowhere else.
	// Short alphanumeric runs (rCB, CD5, KD5, ND5 ...) are internal ids, not user-chosen names,
	// so a length floor removes the noise without hiding real variable names.
	for _, id := range graphIDs {
		if isNoise(id) || len(id) < 4 {
			continue
		}
		inGraphs := countOccurrences(graphText, id)
		elsewhere := countOccurrences(whole, id) - inGraphs
		if elsewhere > 0 {
			continue
		}
		add("warn", "dangling-reference",
			fmt.Sprintf("`%s` 只出现在节点图里（%d 次），文件其它部分（元件/实体/关卡定义）**找不到** —— 请确认它是否已配到某个组件上", id, inGraphs))
	}

	// CJK tokens: a token that is part of a graph name is expected to live only in graphs.
	for _, tok := range graphCJK {
		if isPartOfGraphName(tok, declaredGraphNames) {
			continue
		}
		inGraphs := countOccurrences(graphText, tok)
		elsewhere := countOccurrences(whole, tok) - inGraphs
		if elsewhere > 0 {
			continue
		}
		add("info", "graph-only-name",
			fmt.Sprintf("`%s` 只出现在节点图里（%d 次）：**若是变量名**则属于悬空引用；"+
				"若是复合节点 / 节点自定义标题 / 定时器名则正常", tok, inGraphs))
	}

	// --- names that look like a collision (one is a prefix of another) ---
	all := append(append([]string{}, graphIDs...), graphCJK...)
	sort.Strings(all)
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			a, b := all[i], all[j]
			if a == b {
				continue
			}
			if strings.HasPrefix(b, a) && len([]rune(a)) >= 2 && len([]rune(b))-len([]rune(a)) <= 3 {
				add("warn", "similar-names",
					fmt.Sprintf("`%s` 与 `%s` 高度相似（前缀关系）—— 命名易混，建议统一或区分更明显", a, b))
			}
		}
	}

	// --- naming style mix (snake_case vs camelCase) ---
	hasSnake, hasCamel := false, false
	for _, id := range graphIDs {
		if isNoise(id) || !strings.ContainsAny(id, "_") && !hasUpper(id) {
			continue
		}
		if strings.Contains(id, "_") {
			hasSnake = true
		} else if hasUpper(id) {
			hasCamel = true
		}
	}
	if hasSnake && hasCamel {
		add("info", "naming-style-mix",
			"节点图里同时存在 snake_case 与 camelCase 命名 —— 平台两种都合法，但混用会让“名字写错”变成高频排查项")
	}

	// --- forbidden naming (our own convention: no self/enemy framing) ---
	for _, bad := range lintBannedNames {
		for _, id := range all {
			if strings.EqualFold(id, bad) || strings.HasPrefix(strings.ToLower(id), bad) {
				add("warn", "banned-name",
					fmt.Sprintf("`%s` 命中禁用命名（`%s`）：架构是对称双阵营，命名里不应出现「我方 / 敌方」视角", id, bad))
			}
		}
	}

	// --- per-graph size ---
	for _, g := range graphs {
		n := len(g.Nodes)
		switch {
		case n == 0:
			add("info", "empty-graph", fmt.Sprintf("图 `%s` 没有任何节点", g.Name))
		case n > 3000:
			add("error", "graph-too-large", fmt.Sprintf("图 `%s` 有 %d 个节点，超过平台硬上限 3000", g.Name, n))
		case n > 300:
			// Kept as info on purpose: hard limits are the editor's business (风险检查 may
			// well cover them), so this only nudges when a graph becomes hard to maintain.
			add("info", "graph-large", fmt.Sprintf("图 `%s` 有 %d 个节点，接近可维护上限（平台上限 3000）", g.Name, n))
		}
	}

	// --- custom variables: declared types (the "type written as 整数" class of bug) ---
	lintVariables(raw, knownVarTypes, add)

	if len(findings) == 0 {
		add("info", "clean", "常规检查未发现问题")
	}
	return findings
}

func hasUpper(s string) bool {
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

// trailingOffset extracts the offset from a walk error of the form "... at offset 12345",
// so a stop at the natural end of file is not reported as a problem. Returns -1 if unknown.
func trailingOffset(msg string) int {
	i := strings.LastIndex(msg, "offset ")
	if i < 0 {
		return -1
	}
	n := 0
	seen := false
	for _, r := range msg[i+len("offset "):] {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
		seen = true
	}
	if !seen {
		return -1
	}
	return n
}

// isPartOfGraphName reports whether a CJK token is a fragment of a graph name. The CJK token
// regex splits on ASCII characters, so "关卡-建筑销毁" yields "关卡" and "建筑销毁", and
// "复合节点8" yields "复合节点" -- both must not be mistaken for dangling references.
func isPartOfGraphName(tok string, graphNames map[string]bool) bool {
	for name := range graphNames {
		if strings.Contains(name, tok) {
			return true
		}
	}
	return false
}

// renderLint produces the Markdown lint report.
func renderLint(srcName string, findings []Finding, graphNames []string) string {
	var b strings.Builder
	order := map[string]int{"error": 0, "warn": 1, "info": 2}
	sorted := append([]Finding{}, findings...)
	sort.SliceStable(sorted, func(i, j int) bool { return order[sorted[i].Level] < order[sorted[j].Level] })

	counts := map[string]int{}
	for _, f := range sorted {
		counts[f.Level]++
	}

	fmt.Fprintf(&b, "# 常规检查 - %s\n\n", srcName)
	fmt.Fprintf(&b, "error %d ｜ warn %d ｜ info %d\n\n", counts["error"], counts["warn"], counts["info"])
	b.WriteString("> 🚫 **本报告不替代编辑器自带的检查。** 官方有两道：\n")
	b.WriteString("> **试玩校验**（阻断性错误，会阻止试玩）与 **风险检查**（非阻断提示，多是数据上的错误）。\n")
	b.WriteString("> **官方能查出来的一律以官方为准** —— 本工具只补它们看不到的那些：\n")
	b.WriteString("> 命名约定、跨位置引用的一致性、以及**文件本身**的状态。\n")
	b.WriteString("> ✅ 已实测确认的盲区（保留在本工具里）：**自定义变量类型写错** —— 官方风险检查不报。\n")
	b.WriteString(">\n")
	b.WriteString("> ⚠️ 这些是**待核对项**，不是判定：`.gil` 里没有足够上下文来确证。\n")
	b.WriteString("> 判据说明：某个标识符**只在节点图里出现**、文件其它部分（元件/实体/关卡定义）一次都没有\n")
	b.WriteString("> —— 若它是变量名，就属于「图里用了但没配到任何组件上」。\n\n")
	if len(graphNames) > 0 {
		fmt.Fprintf(&b, "已识别的图名（这些名字只出现在图区属正常）：%s\n\n", strings.Join(graphNames, "、"))
	}
	for _, f := range sorted {
		label := map[string]string{"error": "❌", "warn": "⚠️", "info": "ℹ️"}[f.Level]
		fmt.Fprintf(&b, "- %s **[%s]** %s\n", label, f.Code, f.What)
	}
	return b.String()
}
