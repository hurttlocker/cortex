package ingest

import "testing"

func TestImportKeepDropGate_ScoreAndKeep(t *testing.T) {
	gate, err := NewImportKeepDropGate()
	if err != nil {
		t.Fatalf("NewImportKeepDropGate: %v", err)
	}

	positive := `Decision: keep SQLite + provenance as the default memory substrate.
We should preserve exportability and strong retrieval explainability.`
	negative := `heartbeat ok
session started
connected
gateway status ok`

	posScore := gate.Score(positive)
	negScore := gate.Score(negative)

	if posScore <= negScore {
		t.Fatalf("expected positive score > negative score, got positive=%.4f negative=%.4f", posScore, negScore)
	}
	if gate.Keep(negative) {
		t.Fatalf("expected negative example to fail threshold, score=%.4f threshold=%.4f", negScore, gate.model.Threshold)
	}
}
