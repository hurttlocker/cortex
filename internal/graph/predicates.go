package graph

import "strings"

type predicateRule struct {
	pattern string
	group   string
}

// predicateRules normalizes raw fact predicates to semantic impact groups.
// Order matters: more specific patterns should come first.
var predicateRules = []predicateRule{
	{pattern: "uses strategy", group: "has_strategy"},
	{pattern: "strategy", group: "has_strategy"},
	{pattern: "runbook", group: "has_strategy"},

	{pattern: "configured with", group: "has_config"},
	{pattern: "configures", group: "has_config"},
	{pattern: "config", group: "has_config"},
	{pattern: "setting", group: "has_config"},

	{pattern: "runs on", group: "has_tool"},
	{pattern: "run on", group: "has_tool"},
	{pattern: "uses", group: "has_tool"},
	{pattern: "tool", group: "has_tool"},
	{pattern: "sdk", group: "has_tool"},
	{pattern: "api", group: "has_tool"},

	{pattern: "located at", group: "has_location"},
	{pattern: "located in", group: "has_location"},
	{pattern: "location", group: "has_location"},

	{pattern: "depends on", group: "depends_on"},
	{pattern: "requires", group: "depends_on"},
	{pattern: "blocks", group: "depends_on"},
	{pattern: "blocked by", group: "depends_on"},

	{pattern: "works with", group: "related_to"},
	{pattern: "related to", group: "related_to"},
	{pattern: "connected to", group: "related_to"},
	{pattern: "associated with", group: "related_to"},
}

func normalizePredicateGroup(predicate string) string {
	p := strings.ToLower(strings.TrimSpace(predicate))
	if p == "" {
		return "other"
	}

	for _, rule := range predicateRules {
		if p == rule.pattern || strings.Contains(p, rule.pattern) {
			return rule.group
		}
	}

	return "other"
}
