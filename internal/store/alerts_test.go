package store

import (
	"context"
	"testing"
	"time"
)

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	iface := newTestStore(t)
	ss, ok := iface.(*SQLiteStore)
	if !ok {
		t.Fatal("expected SQLiteStore")
	}
	return ss
}

func TestCreateAndListAlerts(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	// Create a test alert
	alert := &Alert{
		AlertType: AlertTypeConflict,
		Severity:  AlertSeverityWarning,
		Message:   "Test conflict alert",
	}

	err := s.CreateAlert(ctx, alert)
	if err != nil {
		t.Fatalf("CreateAlert: %v", err)
	}
	if alert.ID == 0 {
		t.Fatal("Expected alert ID to be set")
	}

	// List unacknowledged alerts
	unacked := false
	alerts, err := s.ListAlerts(ctx, AlertFilter{Acknowledged: &unacked})
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("Expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Message != "Test conflict alert" {
		t.Fatalf("Expected message 'Test conflict alert', got %q", alerts[0].Message)
	}
	if alerts[0].Acknowledged {
		t.Fatal("Expected alert to be unacknowledged")
	}
}

func TestAcknowledgeAlert(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	alert := &Alert{
		AlertType: AlertTypeConflict,
		Severity:  AlertSeverityInfo,
		Message:   "Conflict to ack",
	}
	s.CreateAlert(ctx, alert)

	// Acknowledge it
	err := s.AcknowledgeAlert(ctx, alert.ID)
	if err != nil {
		t.Fatalf("AcknowledgeAlert: %v", err)
	}

	// Should not appear in unacked list
	unacked := false
	alerts, _ := s.ListAlerts(ctx, AlertFilter{Acknowledged: &unacked})
	if len(alerts) != 0 {
		t.Fatalf("Expected 0 unacked alerts, got %d", len(alerts))
	}

	// Should appear in acked list
	acked := true
	alerts, _ = s.ListAlerts(ctx, AlertFilter{Acknowledged: &acked})
	if len(alerts) != 1 {
		t.Fatalf("Expected 1 acked alert, got %d", len(alerts))
	}
}

func TestCountUnacknowledgedAlerts(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	// Start with 0
	count, _ := s.CountUnacknowledgedAlerts(ctx)
	if count != 0 {
		t.Fatalf("Expected 0, got %d", count)
	}

	// Add 3 alerts
	for i := 0; i < 3; i++ {
		s.CreateAlert(ctx, &Alert{
			AlertType: AlertTypeConflict,
			Severity:  AlertSeverityInfo,
			Message:   "test",
		})
	}

	count, _ = s.CountUnacknowledgedAlerts(ctx)
	if count != 3 {
		t.Fatalf("Expected 3, got %d", count)
	}

	// Ack all
	acked, _ := s.AcknowledgeAllAlerts(ctx, "")
	if acked != 3 {
		t.Fatalf("Expected 3 acked, got %d", acked)
	}

	count, _ = s.CountUnacknowledgedAlerts(ctx)
	if count != 0 {
		t.Fatalf("Expected 0 after ack-all, got %d", count)
	}
}

func TestCheckConflictsForFact(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	// Create a memory
	memID, _ := s.AddMemory(ctx, &Memory{
		Content:    "Test memory",
		SourceFile: "test.md",
	})

	// Create first fact
	fact1 := &Fact{
		MemoryID:  memID,
		Subject:   "Q",
		Predicate: "lives_in",
		Object:    "Philadelphia",
		FactType:  "identity",
	}
	s.AddFact(ctx, fact1)

	// Create second fact with same subject+predicate but different object
	fact2 := &Fact{
		MemoryID:  memID,
		Subject:   "Q",
		Predicate: "lives_in",
		Object:    "New York",
		FactType:  "identity",
	}
	s.AddFact(ctx, fact2)

	// Check conflicts for fact2
	conflicts, err := s.CheckConflictsForFact(ctx, fact2)
	if err != nil {
		t.Fatalf("CheckConflictsForFact: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("Expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Fact2.Object != "Philadelphia" {
		t.Fatalf("Expected conflict with Philadelphia, got %q", conflicts[0].Fact2.Object)
	}
}

func TestCheckConflictsForFact_SameObject_NoConflict(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{
		Content:    "Test",
		SourceFile: "test.md",
	})

	fact1 := &Fact{
		MemoryID: memID, Subject: "Q", Predicate: "lives_in",
		Object: "Philadelphia", FactType: "identity",
	}
	s.AddFact(ctx, fact1)

	fact2 := &Fact{
		MemoryID: memID, Subject: "Q", Predicate: "lives_in",
		Object: "Philadelphia", FactType: "identity",
	}
	s.AddFact(ctx, fact2)

	conflicts, _ := s.CheckConflictsForFact(ctx, fact2)
	if len(conflicts) != 0 {
		t.Fatalf("Expected 0 conflicts for same object, got %d", len(conflicts))
	}
}

func TestAlertFilterByType(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	s.CreateAlert(ctx, &Alert{AlertType: AlertTypeConflict, Severity: AlertSeverityInfo, Message: "conflict1"})
	s.CreateAlert(ctx, &Alert{AlertType: AlertTypeDecay, Severity: AlertSeverityWarning, Message: "decay1"})
	s.CreateAlert(ctx, &Alert{AlertType: AlertTypeConflict, Severity: AlertSeverityCritical, Message: "conflict2"})

	// Filter by conflict only
	alerts, _ := s.ListAlerts(ctx, AlertFilter{Type: AlertTypeConflict})
	if len(alerts) != 2 {
		t.Fatalf("Expected 2 conflict alerts, got %d", len(alerts))
	}

	// Filter by decay only
	alerts, _ = s.ListAlerts(ctx, AlertFilter{Type: AlertTypeDecay})
	if len(alerts) != 1 {
		t.Fatalf("Expected 1 decay alert, got %d", len(alerts))
	}
}

// --- Decay Notification Tests ---

func TestCheckDecayAlerts_NoFading(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	// Create a fresh fact (just reinforced, high confidence)
	memID, _ := s.AddMemory(ctx, &Memory{Content: "fresh", SourceFile: "test.md"})
	s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "Cortex", Predicate: "status", Object: "active",
		FactType: "state", Confidence: 1.0, DecayRate: 0.01,
	})

	result, err := s.CheckDecayAlerts(ctx, DefaultDecayThresholds())
	if err != nil {
		t.Fatalf("CheckDecayAlerts: %v", err)
	}
	if result.FactsScanned != 1 {
		t.Fatalf("Expected 1 fact scanned, got %d", result.FactsScanned)
	}
	if result.AlertsCreated != 0 {
		t.Fatalf("Expected 0 alerts (fresh fact), got %d", result.AlertsCreated)
	}
}

func TestCheckDecayAlerts_WarningThreshold(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "old", SourceFile: "test.md"})

	// Create a fact and backdate its last_reinforced to make it decay below warning
	// With confidence=1.0, decay_rate=0.01, we need e^(-0.01*days) < 0.5
	// -0.01*days < ln(0.5) → days > -ln(0.5)/0.01 → days > 69.3
	factID, err := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "old_config", Predicate: "value", Object: "stale_data",
		FactType: "kv", Confidence: 1.0, DecayRate: 0.01,
	})
	if err != nil {
		t.Fatalf("AddFact: %v", err)
	}

	// Backdate last_reinforced to 80 days ago
	oldTime := time.Now().UTC().AddDate(0, 0, -80)
	s.db.ExecContext(ctx, "UPDATE facts SET last_reinforced = ? WHERE id = ?", oldTime, factID)

	result, err := s.CheckDecayAlerts(ctx, DefaultDecayThresholds())
	if err != nil {
		t.Fatalf("CheckDecayAlerts: %v", err)
	}
	if result.AlertsCreated != 1 {
		t.Fatalf("Expected 1 alert, got %d", result.AlertsCreated)
	}
	if result.WarningCount != 1 {
		t.Fatalf("Expected 1 warning, got %d (critical: %d)", result.WarningCount, result.CriticalCount)
	}

	// Verify the alert was actually created
	unacked := false
	alerts, _ := s.ListAlerts(ctx, AlertFilter{Type: AlertTypeDecay, Acknowledged: &unacked})
	if len(alerts) != 1 {
		t.Fatalf("Expected 1 decay alert in DB, got %d", len(alerts))
	}
	if alerts[0].FactID == nil || *alerts[0].FactID != factID {
		t.Fatalf("Expected alert to reference fact %d", factID)
	}
}

func TestCheckDecayAlerts_CriticalThreshold(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "very old", SourceFile: "test.md"})

	// e^(-0.01*days) < 0.3 → days > -ln(0.3)/0.01 → days > 120.4
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "ancient_config", Predicate: "url", Object: "http://old.example.com",
		FactType: "kv", Confidence: 1.0, DecayRate: 0.01,
	})

	oldTime := time.Now().UTC().AddDate(0, 0, -130)
	s.db.ExecContext(ctx, "UPDATE facts SET last_reinforced = ? WHERE id = ?", oldTime, factID)

	result, err := s.CheckDecayAlerts(ctx, DefaultDecayThresholds())
	if err != nil {
		t.Fatalf("CheckDecayAlerts: %v", err)
	}
	if result.CriticalCount != 1 {
		t.Fatalf("Expected 1 critical, got %d", result.CriticalCount)
	}
}

func TestCheckDecayAlerts_Deduplication(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "dedup test", SourceFile: "test.md"})
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "test", Predicate: "status", Object: "fading",
		FactType: "state", Confidence: 1.0, DecayRate: 0.01,
	})

	oldTime := time.Now().UTC().AddDate(0, 0, -80)
	s.db.ExecContext(ctx, "UPDATE facts SET last_reinforced = ? WHERE id = ?", oldTime, factID)

	// First scan creates alert
	result1, _ := s.CheckDecayAlerts(ctx, DefaultDecayThresholds())
	if result1.AlertsCreated != 1 {
		t.Fatalf("First scan: expected 1 alert, got %d", result1.AlertsCreated)
	}

	// Second scan should skip (already has unacked alert)
	result2, _ := s.CheckDecayAlerts(ctx, DefaultDecayThresholds())
	if result2.AlertsCreated != 0 {
		t.Fatalf("Second scan: expected 0 new alerts (dedup), got %d", result2.AlertsCreated)
	}
	if result2.AlertsSkipped != 1 {
		t.Fatalf("Second scan: expected 1 skipped, got %d", result2.AlertsSkipped)
	}
}

func TestCheckDecayAlerts_AfterAckCreatesNew(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "re-alert test", SourceFile: "test.md"})
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "test", Predicate: "status", Object: "fading",
		FactType: "state", Confidence: 1.0, DecayRate: 0.01,
	})

	oldTime := time.Now().UTC().AddDate(0, 0, -80)
	s.db.ExecContext(ctx, "UPDATE facts SET last_reinforced = ? WHERE id = ?", oldTime, factID)

	// Create first alert
	result1, _ := s.CheckDecayAlerts(ctx, DefaultDecayThresholds())
	if result1.AlertsCreated != 1 {
		t.Fatalf("Expected 1 alert, got %d", result1.AlertsCreated)
	}

	// Acknowledge it
	unacked := false
	alerts, _ := s.ListAlerts(ctx, AlertFilter{Type: AlertTypeDecay, Acknowledged: &unacked})
	s.AcknowledgeAlert(ctx, alerts[0].ID)

	// Scan again — should create new alert since old one was acked
	result2, _ := s.CheckDecayAlerts(ctx, DefaultDecayThresholds())
	if result2.AlertsCreated != 1 {
		t.Fatalf("After ack: expected 1 new alert, got %d", result2.AlertsCreated)
	}
}

func TestCheckDecayAlerts_SupersededExcluded(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "superseded test", SourceFile: "test.md"})
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "old", Predicate: "url", Object: "http://old.com",
		FactType: "kv", Confidence: 1.0, DecayRate: 0.01,
	})

	// Backdate
	oldTime := time.Now().UTC().AddDate(0, 0, -80)
	s.db.ExecContext(ctx, "UPDATE facts SET last_reinforced = ? WHERE id = ?", oldTime, factID)

	// Supersede it
	newFactID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "old", Predicate: "url", Object: "http://new.com",
		FactType: "kv", Confidence: 1.0, DecayRate: 0.01,
	})
	s.SupersedeFact(ctx, factID, newFactID, "updated")

	// Scan — superseded fact should be excluded
	result, _ := s.CheckDecayAlerts(ctx, DefaultDecayThresholds())
	if result.AlertsCreated != 0 {
		t.Fatalf("Expected 0 alerts (superseded fact), got %d", result.AlertsCreated)
	}
}

func TestGetDecayDigest_Grouping(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "digest test", SourceFile: "test.md"})

	// Create 3 facts for same subject, backdate them
	for _, obj := range []string{"val1", "val2", "val3"} {
		fid, _ := s.AddFact(ctx, &Fact{
			MemoryID: memID, Subject: "trading_config", Predicate: "param", Object: obj,
			FactType: "kv", Confidence: 1.0, DecayRate: 0.01,
		})
		oldTime := time.Now().UTC().AddDate(0, 0, -90)
		s.db.ExecContext(ctx, "UPDATE facts SET last_reinforced = ? WHERE id = ?", oldTime, fid)
	}

	// Create 1 fact for different subject
	fid, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "other_thing", Predicate: "status", Object: "unknown",
		FactType: "state", Confidence: 1.0, DecayRate: 0.01,
	})
	oldTime := time.Now().UTC().AddDate(0, 0, -90)
	s.db.ExecContext(ctx, "UPDATE facts SET last_reinforced = ? WHERE id = ?", oldTime, fid)

	groups, err := s.GetDecayDigest(ctx, DefaultDecayThresholds())
	if err != nil {
		t.Fatalf("GetDecayDigest: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("Expected 2 subject groups, got %d", len(groups))
	}

	// Find the trading_config group
	var tradingGroup *DecayDigestGroup
	for i := range groups {
		if groups[i].Subject == "trading_config" {
			tradingGroup = &groups[i]
			break
		}
	}
	if tradingGroup == nil {
		t.Fatal("Expected to find trading_config group")
	}
	if tradingGroup.FactCount != 3 {
		t.Fatalf("Expected 3 facts in trading_config group, got %d", tradingGroup.FactCount)
	}
}

func TestReinforceFromAlert(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "reinforce test", SourceFile: "test.md"})
	factID, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "test", Predicate: "val", Object: "123",
		FactType: "kv", Confidence: 1.0, DecayRate: 0.01,
	})

	// Backdate fact
	oldTime := time.Now().UTC().AddDate(0, 0, -80)
	s.db.ExecContext(ctx, "UPDATE facts SET last_reinforced = ? WHERE id = ?", oldTime, factID)

	// Create decay alert
	s.CheckDecayAlerts(ctx, DefaultDecayThresholds())

	// Get the alert
	unacked := false
	alerts, _ := s.ListAlerts(ctx, AlertFilter{Type: AlertTypeDecay, Acknowledged: &unacked})
	if len(alerts) != 1 {
		t.Fatalf("Expected 1 alert, got %d", len(alerts))
	}

	// Reinforce from alert
	err := s.ReinforceFromAlert(ctx, alerts[0].ID)
	if err != nil {
		t.Fatalf("ReinforceFromAlert: %v", err)
	}

	// Alert should be acknowledged
	unacked2 := false
	remaining, _ := s.ListAlerts(ctx, AlertFilter{Type: AlertTypeDecay, Acknowledged: &unacked2})
	if len(remaining) != 0 {
		t.Fatalf("Expected 0 unacked alerts after reinforce, got %d", len(remaining))
	}

	// Fact should be freshly reinforced
	fact, _ := s.GetFact(ctx, factID)
	timeSinceReinforce := time.Since(fact.LastReinforced)
	if timeSinceReinforce > 5*time.Second {
		t.Fatalf("Fact should be freshly reinforced, but last_reinforced was %v ago", timeSinceReinforce)
	}
}

func TestReinforceFromAlert_WrongType(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	// Create a conflict alert (not decay)
	alert := &Alert{
		AlertType: AlertTypeConflict,
		Severity:  AlertSeverityWarning,
		Message:   "conflict alert",
	}
	s.CreateAlert(ctx, alert)

	// Trying to reinforce from a conflict alert should fail
	err := s.ReinforceFromAlert(ctx, alert.ID)
	if err == nil {
		t.Fatal("Expected error when reinforcing from conflict alert")
	}
}

func TestDecayDigest_SortedByWorst(t *testing.T) {
	s := newTestSQLiteStore(t)
	ctx := context.Background()

	memID, _ := s.AddMemory(ctx, &Memory{Content: "sort test", SourceFile: "test.md"})

	// Create facts with different decay amounts
	// Group A: 130 days old (very low confidence ~0.27)
	fidA, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "GroupA", Predicate: "x", Object: "a",
		FactType: "state", Confidence: 1.0, DecayRate: 0.01,
	})
	s.db.ExecContext(ctx, "UPDATE facts SET last_reinforced = ? WHERE id = ?",
		time.Now().UTC().AddDate(0, 0, -130), fidA)

	// Group B: 75 days old (moderate confidence ~0.47)
	fidB, _ := s.AddFact(ctx, &Fact{
		MemoryID: memID, Subject: "GroupB", Predicate: "x", Object: "b",
		FactType: "state", Confidence: 1.0, DecayRate: 0.01,
	})
	s.db.ExecContext(ctx, "UPDATE facts SET last_reinforced = ? WHERE id = ?",
		time.Now().UTC().AddDate(0, 0, -75), fidB)

	groups, _ := s.GetDecayDigest(ctx, DefaultDecayThresholds())
	if len(groups) != 2 {
		t.Fatalf("Expected 2 groups, got %d", len(groups))
	}
	// Worst (GroupA) should be first
	if groups[0].Subject != "GroupA" {
		t.Fatalf("Expected GroupA first (worst confidence), got %q", groups[0].Subject)
	}
}

func TestDefaultDecayThresholds(t *testing.T) {
	d := DefaultDecayThresholds()
	if d.Warning != 0.5 {
		t.Fatalf("Expected warning 0.5, got %f", d.Warning)
	}
	if d.Critical != 0.3 {
		t.Fatalf("Expected critical 0.3, got %f", d.Critical)
	}
}

func TestTruncateInAlerts(t *testing.T) {
	// Test the shared truncate function used by decay alert messages
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"hi", 2, "hi"},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}
