// Node type ids -> names.
//
// The nine entries below were verified on 2026-10-05 against a screenshot of a real graph
// (关卡-建筑销毁) cross-checked with the extracted node list: node count, type multiplicity and
// relative positions all agreed, and the custom titles matched. Node *names* themselves are
// published in the official docs, so recording the mapping here redistributes nothing
// third-party -- it is our own derivation.
//
// Everything else stays unmapped until evidence arrives; extras go in node-types.txt, which the
// tool only reads. Unmapped types render as "?N" rather than being hidden.
package main

// builtinNodeTypes: verified against our own level, so they hold even without node-types.txt.
var builtinNodeTypes = map[uint64]string{
	373:  "实体销毁时",
	2:    "双分支",
	14:   "是否相等",
	82:   "终止定时器",
	3360: "查询自定义变量快照",
	180:  "数据类型转换",
	3:    "多分支",
	656:  "设置阵营结算成功状态",
	77:   "结算关卡",
}

// The merge of built-ins with node-types.txt lives in loadNodeTypes (graphextract.go).

// destroyEventTypes lists the events the platform only honours on the level entity's graphs.
// Official FAQ: apart from the level entity's node graphs, the entity-destroy / entity-remove
// events do not fire at all. Attaching them to an element fails silently, which is why this
// deserves a lint reminder.
var destroyEventTypes = []string{"实体销毁时", "实体移除时", "实体移除/销毁时"}
