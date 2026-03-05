package reason

import (
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
	_ = query
	return strings.TrimSpace(content)
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
