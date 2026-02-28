package main

import (
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

func TestParseWhereClause(t *testing.T) {
	cases := []string{"confidence>=0.9", "state=active", "source_file~memory/", "exists(source_file)", "imported_at>=2026-02-01"}
	for _, c := range cases {
		if _, err := parseWhereClause(c); err != nil {
			t.Fatalf("parseWhereClause(%q): %v", c, err)
		}
	}
}

func TestMatchWhere_Basic(t *testing.T) {
	rec := queryFactRecord{
		Fact: &store.Fact{ID: 12, Subject: "QQQ", Predicate: "strategy", Object: "ORB", FactType: "decision", Confidence: 0.92, State: "active", CreatedAt: time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC)},
		SourceFile: "memory/2026-02-28.md",
		ImportedAt: time.Date(2026, 2, 28, 12, 1, 0, 0, time.UTC),
	}

	must := func(expr string, want bool) {
		cl, err := parseWhereClause(expr)
		if err != nil {
			t.Fatalf("parse %q: %v", expr, err)
		}
		got := matchWhere(rec, cl)
		if got != want {
			t.Fatalf("match %q: got %v want %v", expr, got, want)
		}
	}

	must("state=active", true)
	must("confidence>=0.9", true)
	must("source_file~memory/", true)
	must("subject=qqq", true)
	must("created_at>=2026-02-28", true)
	must("exists(source_file)", true)
	must("confidence>0.99", false)
}
