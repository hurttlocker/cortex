package temporal

import "testing"

func TestTimestampStartFromSection(t *testing.T) {
	got := TimestampStartFromSection("Session 7 - 7:28 pm on 23 March, 2023")
	if got != "2023-03-23T19:28:00Z" {
		t.Fatalf("TimestampStartFromSection = %q, want %q", got, "2023-03-23T19:28:00Z")
	}
}

func TestNormalizeLiteral_WeekBeforeAbsoluteDate(t *testing.T) {
	n := NormalizeLiteral("the week before 9 June 2023", "")
	if n == nil {
		t.Fatal("expected norm")
	}
	if n.Start != "2023-06-02" || n.End != "2023-06-08" {
		t.Fatalf("got %s..%s, want 2023-06-02..2023-06-08", n.Start, n.End)
	}
}

func TestNormalizeLiteral_LastWeekUsesAnchor(t *testing.T) {
	n := NormalizeLiteral("last week", "2023-03-23T19:28:00Z")
	if n == nil {
		t.Fatal("expected norm")
	}
	if n.Start != "2023-03-16" || n.End != "2023-03-22" {
		t.Fatalf("got %s..%s, want 2023-03-16..2023-03-22", n.Start, n.End)
	}
}
