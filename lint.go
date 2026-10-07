// 对单个导出做常规检查（"lint"）。
//
// 范围**刻意限制在本工具真正看得见的东西上**，而且每条结论都写成"待核对项"而不是判定 ——
// `.gil` 里没有足够上下文来确证（只出现在节点图里的标识符，完全可能是图名、节点自定义标题或定时器名）。
//
// 核心判据简单且可度量：对每个出现在节点图区里的标识符，它在文件**其它部分**（元件 / 实体 /
// 关卡定义）出现多少次？图里用了、但没有任何地方声明的变量，就会表现为
// "图里 > 0，别处 == 0" —— 正是"悬空引用"那一种情况。
package main

import (
	"fmt"
	"sort"
	"strings"
)

// Finding 是一条检查结果。Level 取 "error" / "warn" / "info"。
type Finding struct {
	Level string
	Code  string
	What  string
}

// 这些是结构性噪声（骨骼挂点 / 资源名前缀），不是用户起的名字
var lintNoisePrefixes = []string{
	"GI_", "PRIVATE_", "Beyd_", "Fx_", "MPAction", "Root", "root", "FocusAnchor",
	"MoveHead", "Foot", "Hand", "Weapon", "Chest", "Head", "Neck", "Body", "Waist", "Knee",
	"Billboard", "RUNE", "Bone", "Attach",
}

// 我们自己的命名约定禁止的名字（读起来像"一方就是玩家"）
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

	// 缺少图字段**不能**中断与图无关的检查
	// （完全没有节点图的关卡，也要照常检查变量声明）。
	graphPayload, hasGraphs := fieldPayload(raw, graphField)
	if !hasGraphs {
		add("error", "parse-graph-field", fmt.Sprintf("容器字段 f%d 找不到或不是长度分隔字段：无法检查节点图（变量检查仍会执行）", graphField))
	}
	if _, walkErr := parseFields(raw, 20); walkErr != "" {
		// 实测导出文件末尾本来就有几个字节不是合法字段（例如 85267 字节的文件"stopped at
		// offset 85264"）。只有**提前很多**停下才算可疑。
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

	// --- 候选标识符：节点图里引用到的 ---
	graphIDs := uniqSorted(matches(idRe, graphText))
	graphCJK := uniqSorted(matches(cjkRe, graphText))

	declaredGraphNames := map[string]bool{}
	for _, g := range graphs {
		if g.Name != "" {
			declaredGraphNames[g.Name] = true
		}
	}

	// ASCII 标识符：只在图里出现、别处没有。
	// 短的字母数字串（rCB、CD5、KD5、ND5 …）是内部 id，不是用户起的名字，
	// 所以设一个长度下限来去噪，同时不会把真正的变量名藏掉。
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

	// 中文词：属于图名一部分的词，本来就只出现在图区。
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

	// --- 疑似撞名：一个名字是另一个的前缀 ---
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

	// --- 命名风格混用（snake_case 与 camelCase）---
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

	// --- 禁用命名（我们自己的约定：不要"我方 / 敌方"视角）---
	for _, bad := range lintBannedNames {
		for _, id := range all {
			if strings.EqualFold(id, bad) || strings.HasPrefix(strings.ToLower(id), bad) {
				add("warn", "banned-name",
					fmt.Sprintf("`%s` 命中禁用命名（`%s`）：架构是对称双阵营，命名里不应出现「我方 / 敌方」视角", id, bad))
			}
		}
	}

	// --- 每张图的规模 ---
	for _, g := range graphs {
		n := len(g.Nodes)
		switch {
		case n == 0:
			add("info", "empty-graph", fmt.Sprintf("图 `%s` 没有任何节点", g.Name))
		case n > 3000:
			add("error", "graph-too-large", fmt.Sprintf("图 `%s` 有 %d 个节点，超过平台硬上限 3000", g.Name, n))
		case n > 300:
			// 刻意保持 info 级别：硬上限是编辑器的事（官方风险检查很可能已经覆盖），
			// 这里只在图开始**难以维护**时轻提示一下。
			add("info", "graph-large", fmt.Sprintf("图 `%s` 有 %d 个节点，接近可维护上限（平台上限 3000）", g.Name, n))
		}
	}

	// --- 自定义变量：声明的类型（"类型写成整数"那一类坑）---
	lintVariables(raw, knownVarTypes, add)

	// --- 销毁事件的挂载位置（**提醒**，不是判定）---
	// 平台只在【关卡实体】的图上触发 实体销毁时 / 实体移除。我们读不出"图挂在哪个实体上"
	// （见 docs/node-graph-extraction.md：图 id 是局部作用域，文件里别处从未引用），
	// 所以这里只能给提醒 —— 但正是这条提醒，能抓住"挂到元件上、然后什么都没发生"那种情况。
	nodeTypes := loadNodeTypes("node-types.txt")
	for _, g := range graphs {
		for _, n := range g.Nodes {
			name := nodeTypes[n.TypeID]
			isDestroy := false
			for _, d := range destroyEventTypes {
				if name == d {
					isDestroy = true
					break
				}
			}
			if !isDestroy {
				continue
			}
			add("warn", "destroy-event-placement",
				fmt.Sprintf("图 `%s` 里用了【%s】：该事件**只在关卡实体的节点图上生效**（官方 FAQ）—— "+
					"若这张图挂在元件 / 建筑上，销毁时会**静默不触发**。工具读不到图的挂载主体，请自行确认。", g.Name, name))
			break
		}
	}

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

// trailingOffset 从形如 "... at offset 12345" 的走查错误里取出偏移量，
// 这样"停在文件自然结尾"就不会被当成问题。取不到时返回 -1。
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

// isPartOfGraphName 判断某个中文词是不是**图名的片段**。中文分词正则会按 ASCII 字符切开，
// 所以 "关卡-建筑销毁" 会切出 "关卡" 与 "建筑销毁"，"复合节点8" 会切出 "复合节点" ——
// 这两者都不能被误判成悬空引用。
func isPartOfGraphName(tok string, graphNames map[string]bool) bool {
	for name := range graphNames {
		if strings.Contains(name, tok) {
			return true
		}
	}
	return false
}

// renderLint 生成 Markdown 格式的检查报告。
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
