package store

import (
	"context"
	"testing"
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
