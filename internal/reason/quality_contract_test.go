package reason

import (
	"strings"
	"testing"
)

func TestNeedsContractRepair(t *testing.T) {
	good := `## Summary
This is a sufficiently long summary with context and operational detail so it is not considered too short. It includes references to risk, impact, and decision outcomes to keep usefulness high.

## Evidence
- Source: memory and fact review completed.
- Fact confidence: key facts validated with confidence tags.

## Conflicts & Trade-offs
- Uncertain: one source may be stale and needs verification.
- Trade-off: speed vs depth is an active conflict.

## Next Actions
- Priority: High | Owner: A | Timeline: Today | Recommendation: Verify source links. | Impact: Better grounding.
- Priority: High | Owner: A | Timeline: Tomorrow | Recommendation: Patch weak prompt terms. | Impact: Better actionability.
- Priority: Medium | Owner: A | Timeline: This week | Recommendation: Re-run eval and guardrails. | Impact: Fewer regressions.
`
	if needsContractRepair(good) {
		t.Fatal("expected good content not to need repair")
	}

	bad := "Too short"
	if !needsContractRepair(bad) {
		t.Fatal("expected short content to need repair")
	}
}

func TestEnforceResponseQualityContract_Repairs(t *testing.T) {
	out := enforceResponseQualityContract("brief", "test query")
	for _, h := range []string{"## Summary", "## Evidence", "## Conflicts & Trade-offs", "## Next Actions"} {
		if !strings.Contains(out, h) {
			t.Fatalf("missing required header %s", h)
		}
	}
	for _, label := range []string{"Priority:", "Owner:", "Timeline:", "Recommendation:", "Impact:"} {
		if !strings.Contains(out, label) {
			t.Fatalf("missing required label %s", label)
		}
	}
}
