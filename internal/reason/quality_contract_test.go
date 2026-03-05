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

func TestEnforceResponseQualityContract_LeavesModelOutputUntouched(t *testing.T) {
	in := "brief"
	out := enforceResponseQualityContract(in, "test query")
	if strings.TrimSpace(out) != in {
		t.Fatalf("expected output to remain unchanged, got %q", out)
	}
}
