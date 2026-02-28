package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

type whereOp string

const (
	opEq       whereOp = "eq"
	opNe       whereOp = "ne"
	opGt       whereOp = "gt"
	opLt       whereOp = "lt"
	opGte      whereOp = "gte"
	opLte      whereOp = "lte"
	opContains whereOp = "contains"
	opExists   whereOp = "exists"
)

type whereClause struct {
	Field string
	Op    whereOp
	Value string
	Raw   string
}

type queryFactRecord struct {
	Fact       *store.Fact `json:"fact"`
	SourceFile string      `json:"source_file,omitempty"`
	ImportedAt time.Time   `json:"imported_at,omitempty"`
}

func runQuery(args []string) error {
	wheres := make([]whereClause, 0)
	format := ""
	limit := 100
	includeSuperseded := false

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--where" && i+1 < len(args):
			i++
			cl, err := parseWhereClause(args[i])
			if err != nil {
				return err
			}
			wheres = append(wheres, cl)
		case strings.HasPrefix(args[i], "--where="):
			cl, err := parseWhereClause(strings.TrimPrefix(args[i], "--where="))
			if err != nil {
				return err
			}
			wheres = append(wheres, cl)
		case args[i] == "--format" && i+1 < len(args):
			i++
			format = strings.ToLower(strings.TrimSpace(args[i]))
		case strings.HasPrefix(args[i], "--format="):
			format = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(args[i], "--format=")))
		case args[i] == "--limit" && i+1 < len(args):
			i++
			fmt.Sscanf(args[i], "%d", &limit)
		case strings.HasPrefix(args[i], "--limit="):
			fmt.Sscanf(strings.TrimPrefix(args[i], "--limit="), "%d", &limit)
		case args[i] == "--include-superseded":
			includeSuperseded = true
		case strings.HasPrefix(args[i], "-"):
			return fmt.Errorf("unknown flag: %s", args[i])
		default:
			return fmt.Errorf("unexpected argument: %s", args[i])
		}
	}

	if len(wheres) == 0 {
		return fmt.Errorf("usage: cortex query --where <expr> [--where <expr> ...] [--format json|table|list] [--limit N] [--include-superseded]")
	}
	if limit <= 0 {
		limit = 100
	}

	s, err := store.NewStore(getStoreConfig())
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer s.Close()
	ctx := context.Background()

	facts, err := s.ListFacts(ctx, store.ListOpts{Limit: math.MaxInt32, IncludeSuperseded: includeSuperseded})
	if err != nil {
		return fmt.Errorf("listing facts: %w", err)
	}
	memories, _ := s.ListMemories(ctx, store.ListOpts{Limit: math.MaxInt32})
	memoryMeta := make(map[int64]store.Memory, len(memories))
	for _, m := range memories {
		memoryMeta[m.ID] = *m
	}

	rows := make([]queryFactRecord, 0, len(facts))
	for _, f := range facts {
		rec := queryFactRecord{Fact: f}
		if m, ok := memoryMeta[f.MemoryID]; ok {
			rec.SourceFile = m.SourceFile
			rec.ImportedAt = m.ImportedAt
		}
		if matchesWhereAll(rec, wheres) {
			rows = append(rows, rec)
			if len(rows) >= limit {
				break
			}
		}
	}

	if format == "" {
		if isTTY() {
			format = "table"
		} else {
			format = "json"
		}
	}

	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	case "list":
		for _, r := range rows {
			fmt.Printf("- #%d [%s/%s] %.2f %s — %s: %s\n", r.Fact.ID, r.Fact.FactType, r.Fact.State, r.Fact.Confidence, r.Fact.Subject, r.Fact.Predicate, r.Fact.Object)
			if r.SourceFile != "" {
				fmt.Printf("  source: %s\n", r.SourceFile)
			}
		}
		fmt.Printf("\n%d rows\n", len(rows))
		return nil
	case "table":
		fmt.Printf("%-6s %-10s %-10s %-6s %-24s %-28s\n", "ID", "TYPE", "STATE", "CONF", "SOURCE", "SUBJECT")
		for _, r := range rows {
			src := r.SourceFile
			if len(src) > 24 {
				src = "…" + src[len(src)-23:]
			}
			subj := r.Fact.Subject
			if len(subj) > 28 {
				subj = subj[:27] + "…"
			}
			fmt.Printf("%-6d %-10s %-10s %-6.2f %-24s %-28s\n", r.Fact.ID, r.Fact.FactType, r.Fact.State, r.Fact.Confidence, src, subj)
		}
		fmt.Printf("\n%d rows\n", len(rows))
		return nil
	default:
		return fmt.Errorf("unsupported format: %s (supported: json, table, list)", format)
	}
}

func parseWhereClause(raw string) (whereClause, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return whereClause{}, fmt.Errorf("empty --where clause")
	}
	lc := strings.ToLower(raw)
	if strings.HasPrefix(lc, "exists(") && strings.HasSuffix(raw, ")") {
		field := strings.TrimSpace(raw[len("exists(") : len(raw)-1])
		if field == "" {
			return whereClause{}, fmt.Errorf("invalid exists() clause: %s", raw)
		}
		return whereClause{Field: normalizeWhereField(field), Op: opExists, Raw: raw}, nil
	}
	if strings.HasSuffix(lc, " exists") {
		field := strings.TrimSpace(raw[:len(raw)-len(" exists")])
		if field == "" {
			return whereClause{}, fmt.Errorf("invalid exists clause: %s", raw)
		}
		return whereClause{Field: normalizeWhereField(field), Op: opExists, Raw: raw}, nil
	}

	ops := []struct {
		tok string
		op  whereOp
	}{{">=", opGte}, {"<=", opLte}, {"!=", opNe}, {"~", opContains}, {">", opGt}, {"<", opLt}, {"=", opEq}}
	for _, c := range ops {
		if idx := strings.Index(raw, c.tok); idx > 0 {
			field := normalizeWhereField(strings.TrimSpace(raw[:idx]))
			value := strings.TrimSpace(raw[idx+len(c.tok):])
			if field == "" || value == "" {
				return whereClause{}, fmt.Errorf("invalid where clause: %s", raw)
			}
			return whereClause{Field: field, Op: c.op, Value: value, Raw: raw}, nil
		}
	}
	return whereClause{}, fmt.Errorf("invalid where clause %q (use field=value, field>=N, field~text, exists(field))", raw)
}

func normalizeWhereField(f string) string {
	f = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(f, "-", "_")))
	switch f {
	case "type":
		return "fact_type"
	case "source":
		return "source_file"
	case "imported":
		return "imported_at"
	default:
		return f
	}
}

func matchesWhereAll(rec queryFactRecord, where []whereClause) bool {
	for _, w := range where {
		if !matchWhere(rec, w) {
			return false
		}
	}
	return true
}

func matchWhere(rec queryFactRecord, w whereClause) bool {
	val, ok := fieldValue(rec, w.Field)
	if !ok {
		return false
	}
	if w.Op == opExists {
		return strings.TrimSpace(val) != ""
	}

	if isNumericField(w.Field) {
		lhs, err1 := strconv.ParseFloat(val, 64)
		rhs, err2 := strconv.ParseFloat(w.Value, 64)
		if err1 != nil || err2 != nil {
			return false
		}
		switch w.Op {
		case opEq:
			return lhs == rhs
		case opNe:
			return lhs != rhs
		case opGt:
			return lhs > rhs
		case opLt:
			return lhs < rhs
		case opGte:
			return lhs >= rhs
		case opLte:
			return lhs <= rhs
		}
		return false
	}

	if isTimeField(w.Field) {
		lhs, ok := parseTimeForWhere(val)
		rhs, ok2 := parseTimeForWhere(w.Value)
		if !ok || !ok2 {
			return false
		}
		switch w.Op {
		case opEq:
			return lhs.Equal(rhs)
		case opNe:
			return !lhs.Equal(rhs)
		case opGt:
			return lhs.After(rhs)
		case opLt:
			return lhs.Before(rhs)
		case opGte:
			return lhs.After(rhs) || lhs.Equal(rhs)
		case opLte:
			return lhs.Before(rhs) || lhs.Equal(rhs)
		}
		return false
	}

	lhs := strings.ToLower(strings.TrimSpace(val))
	rhs := strings.ToLower(strings.TrimSpace(w.Value))
	switch w.Op {
	case opEq:
		return lhs == rhs
	case opNe:
		return lhs != rhs
	case opContains:
		return strings.Contains(lhs, rhs)
	case opGt, opLt, opGte, opLte:
		if lhs == "" || rhs == "" {
			return false
		}
		cmp := strings.Compare(lhs, rhs)
		switch w.Op {
		case opGt:
			return cmp > 0
		case opLt:
			return cmp < 0
		case opGte:
			return cmp >= 0
		case opLte:
			return cmp <= 0
		}
	}
	return false
}

func fieldValue(rec queryFactRecord, field string) (string, bool) {
	f := rec.Fact
	switch field {
	case "id":
		return strconv.FormatInt(f.ID, 10), true
	case "memory_id":
		return strconv.FormatInt(f.MemoryID, 10), true
	case "subject":
		return f.Subject, true
	case "predicate":
		return f.Predicate, true
	case "object":
		return f.Object, true
	case "fact_type":
		return f.FactType, true
	case "state":
		return f.State, true
	case "confidence":
		return fmt.Sprintf("%f", f.Confidence), true
	case "source_quote":
		return f.SourceQuote, true
	case "source_file":
		return rec.SourceFile, true
	case "agent", "agent_id":
		return f.AgentID, true
	case "created_at":
		return f.CreatedAt.Format(time.RFC3339Nano), true
	case "last_reinforced":
		return f.LastReinforced.Format(time.RFC3339Nano), true
	case "imported_at":
		if rec.ImportedAt.IsZero() {
			return "", true
		}
		return rec.ImportedAt.Format(time.RFC3339Nano), true
	default:
		return "", false
	}
}

func isNumericField(field string) bool {
	switch field {
	case "id", "memory_id", "confidence":
		return true
	default:
		return false
	}
}

func isTimeField(field string) bool {
	switch field {
	case "created_at", "last_reinforced", "imported_at":
		return true
	default:
		return false
	}
}

func parseTimeForWhere(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
