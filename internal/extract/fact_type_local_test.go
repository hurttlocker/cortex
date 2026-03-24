package extract

import "testing"

func TestLocalFactTypeClassifier_PredictsObviousSemanticTypes(t *testing.T) {
	classifier, err := NewLocalFactTypeClassifier()
	if err != nil {
		t.Fatalf("NewLocalFactTypeClassifier: %v", err)
	}

	cases := []struct {
		name string
		fact ClassifyableFact
		want string
	}{
		{
			name: "identity email",
			fact: ClassifyableFact{Subject: "Alice", Predicate: "email", Object: "alice@example.com", FactType: "kv"},
			want: "identity",
		},
		{
			name: "location branch",
			fact: ClassifyableFact{Subject: "repo", Predicate: "branch", Object: "fix/plugin-recall-session-bias", FactType: "kv"},
			want: "location",
		},
		{
			name: "event shipped",
			fact: ClassifyableFact{Subject: "mobile", Predicate: "shipped", Object: "slash command support", FactType: "kv"},
			want: "event",
		},
		{
			name: "rule operational",
			fact: ClassifyableFact{Subject: "ops runbook", Predicate: "operational rule", Object: "always run go test ./... before pushing", FactType: "kv"},
			want: "rule",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, confidence := classifier.Predict(tc.fact)
			if got != tc.want {
				t.Fatalf("Predict() = %q, want %q", got, tc.want)
			}
			if confidence <= 0 {
				t.Fatalf("Predict() confidence = %.3f, want > 0", confidence)
			}
		})
	}
}

func TestClassifyFactsLocal_ReclassifiesKVFacts(t *testing.T) {
	facts := []ClassifyableFact{
		{ID: 1, Subject: "Alice", Predicate: "email", Object: "alice@example.com", FactType: "kv"},
		{ID: 2, Subject: "repo", Predicate: "branch", Object: "feat/import-quality-gate", FactType: "kv"},
	}
	result, err := ClassifyFactsLocal(facts, ClassifyOpts{MinConfidence: 0.45})
	if err != nil {
		t.Fatalf("ClassifyFactsLocal: %v", err)
	}
	if len(result.Classified) != 2 {
		t.Fatalf("expected 2 reclassifications, got %d", len(result.Classified))
	}
	if result.Classified[0].NewType == "kv" || result.Classified[1].NewType == "kv" {
		t.Fatalf("expected non-kv predictions, got %+v", result.Classified)
	}
}
