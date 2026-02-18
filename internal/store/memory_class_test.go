package store

import (
	"reflect"
	"testing"
)

func TestParseMemoryClassList(t *testing.T) {
	got, err := ParseMemoryClassList("rule, decision,rule, preference")
	if err != nil {
		t.Fatalf("ParseMemoryClassList error: %v", err)
	}
	want := []string{"rule", "decision", "preference"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseMemoryClassList_Invalid(t *testing.T) {
	_, err := ParseMemoryClassList("rule,unknown")
	if err == nil {
		t.Fatal("expected invalid class error")
	}
}

func TestNormalizeMemoryClass(t *testing.T) {
	if got := NormalizeMemoryClass("  DECISION  "); got != "decision" {
		t.Fatalf("NormalizeMemoryClass = %q, want decision", got)
	}
}
