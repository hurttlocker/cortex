package reason

import (
	"strings"
	"testing"
)

const compliantReasonOutput = `## Summary
This is a sufficiently long summary with context and operational detail so it is not considered too short. It includes references to risk, impact, and decision outcomes to keep usefulness high. It also states execution assumptions so operators can verify what is known versus what still needs confirmation.

## Evidence
- Source: memory and fact review completed.
- Fact confidence: key facts validated with confidence tags.
- Provenance note: references are consistent with the latest indexed workspace snapshots and no obvious stale source was promoted as current truth.

## Conflicts & Trade-offs
- Uncertain: one source may be stale and needs verification.
- Trade-off: speed vs depth is an active conflict.
- Constraint: raising confidence requires additional source checks that slow immediate execution.

## Next Actions
- Priority: High | Owner: A | Timeline: Today | Recommendation: Verify source links. | Impact: Better grounding.
- Priority: High | Owner: A | Timeline: Tomorrow | Recommendation: Patch weak prompt terms. | Impact: Better actionability.
- Priority: Medium | Owner: A | Timeline: This week | Recommendation: Re-run eval and guardrails. | Impact: Fewer regressions.
`

func TestNeedsContractRepair(t *testing.T) {
	if needsContractRepair(compliantReasonOutput) {
		t.Fatal("expected good content not to need repair")
	}

	bad := "Too short"
	if !needsContractRepair(bad) {
		t.Fatal("expected short content to need repair")
	}
}

func TestEnforceResponseQualityContract_PreservesCompliantOutput(t *testing.T) {
	out := enforceResponseQualityContract(compliantReasonOutput, "operator follow-up")
	if strings.TrimSpace(out) != strings.TrimSpace(compliantReasonOutput) {
		t.Fatalf("expected compliant output to remain unchanged")
	}
}

func TestEnforceResponseQualityContract_RepairsThinOutput(t *testing.T) {
	out := enforceResponseQualityContract("brief", "weekly architecture review")

	for _, expected := range []string{
		"## Summary",
		"## Evidence",
		"## Conflicts & Trade-offs",
		"## Next Actions",
		"Priority:",
		"Owner:",
		"Timeline:",
		"Recommendation:",
		"Impact:",
		"weekly architecture review",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected repaired output to contain %q\noutput:\n%s", expected, out)
		}
	}

	if !nextActionBulletRE.MatchString(sectionBody(out, "## Next Actions")) {
		t.Fatalf("expected repaired next actions to include bullet lines")
	}
}
