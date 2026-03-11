package reason

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	requiredResponseHeaders = []string{
		"## Summary",
		"## Evidence",
		"## Conflicts & Trade-offs",
		"## Next Actions",
	}
	nextActionBulletRE = regexp.MustCompile(`(?m)^\s*(?:[-*]|\d+\.)\s+`)
)

func enforceResponseQualityContract(content, query string) string {
	cleaned := strings.TrimSpace(content)
	if !needsContractRepair(cleaned) {
		return cleaned
	}
	return buildContractRepair(cleaned, query)
}

func needsContractRepair(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return true
	}

	if len(strings.Fields(content)) < 120 {
		return true
	}

	lower := strings.ToLower(content)
	for _, h := range requiredResponseHeaders {
		if !strings.Contains(lower, strings.ToLower(h)) {
			return true
		}
	}

	for _, label := range []string{"priority:", "owner:", "timeline:", "recommendation:", "impact:"} {
		if !strings.Contains(lower, label) {
			return true
		}
	}

	nextActions := sectionBody(content, "## Next Actions")
	if len(nextActionBulletRE.FindAllString(nextActions, -1)) < 3 {
		return true
	}

	return false
}

func sectionBody(content, header string) string {
	idx := strings.Index(strings.ToLower(content), strings.ToLower(header))
	if idx < 0 {
		return ""
	}
	body := content[idx+len(header):]
	if next := strings.Index(strings.ToLower(body), "\n## "); next >= 0 {
		body = body[:next]
	}
	return body
}

func buildContractRepair(content, query string) string {
	excerpt := summarizeSnippet(content, 420)
	if excerpt == "" {
		excerpt = "No substantive model output was produced."
	}

	focus := summarizeSnippet(query, 140)
	if focus == "" {
		focus = "operator request"
	}

	var sb strings.Builder
	sb.WriteString("## Summary\n")
	sb.WriteString("The original response was too thin or structurally incomplete for operator use, so this repaired output preserves intent while restoring the required decision-ready format. ")
	sb.WriteString(fmt.Sprintf("Query focus: %q.\n\n", focus))

	sb.WriteString("## Evidence\n")
	sb.WriteString("- Recovered source excerpt: \"")
	sb.WriteString(excerpt)
	sb.WriteString("\"\n")
	sb.WriteString("- Structural gate outcome: required headers/action labels were missing or incomplete, which reduced actionability confidence.\n")
	sb.WriteString("- Confidence status: Uncertain until evidence claims are validated directly against source memories/facts.\n\n")

	sb.WriteString("## Conflicts & Trade-offs\n")
	sb.WriteString("- Uncertain: this repair adds deterministic structure, but cannot invent missing evidence beyond the original content.\n")
	sb.WriteString("- Trade-off: enforcing structure improves execution readiness, while preserving the excerpt avoids hallucinating unsupported claims.\n")
	sb.WriteString("- Risk: acting without validation can still propagate stale context, so verification remains mandatory.\n\n")

	sb.WriteString("## Next Actions\n")
	sb.WriteString("- Priority: High | Owner: Operator | Timeline: Now | Recommendation: Re-run `cortex reason` for the same prompt and compare sections against this repaired draft before execution. | Impact: Confirms current context and reduces hard-failure risk.\n")
	sb.WriteString("- Priority: High | Owner: Operator | Timeline: Today | Recommendation: Validate key claims against cited memories/facts and mark anything unresolved as Uncertain. | Impact: Improves factual grounding and contradiction safety.\n")
	sb.WriteString("- Priority: Medium | Owner: Operator | Timeline: Today | Recommendation: Convert accepted recommendations into a checklist with explicit assignees and due times. | Impact: Raises actionability and execution follow-through.\n")
	sb.WriteString("- Priority: Medium | Owner: Operator | Timeline: This week | Recommendation: Capture missing constraints discovered during validation and feed them into the next reason-quality eval pass. | Impact: Prevents repeat low-structure outputs.\n")

	return strings.TrimSpace(sb.String())
}

func summarizeSnippet(text string, maxLen int) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if maxLen <= 0 || len(trimmed) <= maxLen {
		return trimmed
	}
	if maxLen <= 3 {
		return trimmed[:maxLen]
	}
	return trimmed[:maxLen-3] + "..."
}
