// SPDX-License-Identifier: MIT

package provider

import (
	"encoding/json"
	"strings"
)

// effortRank orders the reasoning efforts that Codex and providers name.
var effortRank = map[string]int{"none": 0, "minimal": 1, "low": 2, "medium": 3, "high": 4, "xhigh": 5, "max": 6, "ultra": 7}

// defaultMaxReasoningEffort is the highest effort a provider takes unless its
// profile declares another: most Chat Completions providers stop at high.
const defaultMaxReasoningEffort = "high"

// wireReasoningEffort lowers an effort above the profile's highest one to it;
// an effort it does not know stays as it is.
func wireReasoningEffort(effort, max string) string {
	if max == "" {
		max = defaultMaxReasoningEffort
	}
	if rank, known := effortRank[strings.ToLower(strings.TrimSpace(effort))]; known && rank > effortRank[max] {
		return max
	}
	return effort
}

// normalizeWireReasoning changes only effort fields in an outgoing JSON object.
// Callers own this object; request policy, reports and configuration stay intact.
func normalizeWireReasoning(object map[string]json.RawMessage, max string) bool {
	changed := false
	rewrite := func(object map[string]json.RawMessage, field string) bool {
		var effort string
		if json.Unmarshal(object[field], &effort) != nil {
			return false
		}
		wire := wireReasoningEffort(effort, max)
		if wire == effort {
			return false
		}
		object[field] = mustJSON(wire)
		return true
	}
	for _, field := range []string{"reasoning_effort", "reasoningEffort", "thinking"} {
		changed = rewrite(object, field) || changed
	}
	for _, field := range []string{"reasoning", "thinking"} {
		var nested map[string]json.RawMessage
		if json.Unmarshal(object[field], &nested) == nil && rewrite(nested, "effort") {
			object[field] = mustJSON(nested)
			changed = true
		}
	}
	return changed
}
