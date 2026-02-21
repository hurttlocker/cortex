package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/extract"
	_ "modernc.org/sqlite"
)

type groupedFactRow struct {
	MemoryID      int64
	Content       []byte
	SourceFile    string
	SourceSection string
	ImportedAt    string
	Predicate     string
	Count         int64
}

type memoryRow struct {
	ID            int64
	Content       string
	SourceFile    string
	SourceSection string
	ImportedAt    string
	NoisyFacts    int64
}

type metrics struct {
	FactsTotal       int64            `json:"facts_total"`
	FactsByType      map[string]int64 `json:"facts_by_type"`
	KVFacts          int64            `json:"kv_facts"`
	NoisyKVFacts     int64            `json:"noisy_kv_facts"`
	NoisyKVPct       float64          `json:"noisy_kv_pct"`
	DistinctMemories int64            `json:"distinct_memories"`
}

type report struct {
	DBPath        string        `json:"db_path"`
	GeneratedAt   time.Time     `json:"generated_at"`
	Mode          string        `json:"mode"`
	Limit         int           `json:"limit"`
	Offset        int           `json:"offset"`
	Since         string        `json:"since,omitempty"`
	Selected      int           `json:"selected_memories"`
	SelectedNoisy int64         `json:"selected_noisy_kv_facts"`
	BackupPath    string        `json:"backup_path,omitempty"`
	GlobalBefore  metrics       `json:"global_before"`
	GlobalAfter   metrics       `json:"global_after"`
	SubsetBefore  metrics       `json:"subset_before"`
	SubsetAfter   metrics       `json:"subset_after"`
	Processed     int           `json:"processed"`
	Failed        int           `json:"failed"`
	DeletedFacts  int64         `json:"deleted_facts"`
	InsertedFacts int64         `json:"inserted_facts"`
	TopSources    []sourceCount `json:"top_sources,omitempty"`
	Errors        []string      `json:"errors,omitempty"`
}

type sourceCount struct {
	Source     string `json:"source"`
	NoisyFacts int64  `json:"noisy_facts"`
}

var nonAlphaNumRE = regexp.MustCompile(`[^a-z0-9]+`)
var transcriptRoleLineRE = regexp.MustCompile(`(?im)^\s*(assistant|user|system)\s*:`)

func normalizePredicate(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(s, "\"'`[]{}()")
	s = nonAlphaNumRE.ReplaceAllString(s, "")
	return s
}

func isAutoCapturePath(path string) bool {
	p := strings.ToLower(strings.TrimSpace(path))
	return strings.Contains(p, "auto-capture") || strings.Contains(p, "cortex-capture-")
}

func isTranscriptLike(content string) bool {
	lower := strings.ToLower(content)
	if strings.Contains(lower, "<cortex-memories>") ||
		strings.Contains(lower, "(untrusted metadata)") ||
		strings.Contains(lower, "conversation info (untrusted metadata)") ||
		strings.Contains(lower, "sender (untrusted metadata)") ||
		strings.Contains(lower, "[message_id:") ||
		strings.Contains(lower, "[queued messages while agent was busy]") {
		return true
	}
	return len(transcriptRoleLineRE.FindAllStringIndex(content, -1)) >= 2
}

func noisyPredicateSet() map[string]struct{} {
	keys := []string{
		"conversationlabel", "groupsubject", "groupchannel", "groupspace",
		"sender", "label", "username", "tag", "currenttime", "messageid",
		"assistant", "user", "system",
	}
	out := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		out[k] = struct{}{}
	}
	return out
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := strings.Builder{}
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("?")
	}
	return b.String()
}

func collectCandidates(ctx context.Context, db *sql.DB, since string, limit, offset int) ([]memoryRow, []sourceCount, error) {
	args := []any{}
	query := `
SELECT m.id, CAST(m.content AS BLOB), m.source_file, m.source_section, m.imported_at, f.predicate, COUNT(*)
FROM memories m
JOIN facts f ON f.memory_id = m.id
WHERE f.fact_type = 'kv'
`
	if since != "" {
		query += "  AND m.imported_at >= ?\n"
		args = append(args, since)
	}
	query += "GROUP BY m.id, f.predicate ORDER BY m.imported_at DESC"

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	noisy := noisyPredicateSet()
	memMap := map[int64]*memoryRow{}
	sourceNoise := map[string]int64{}

	for rows.Next() {
		var r groupedFactRow
		if err := rows.Scan(&r.MemoryID, &r.Content, &r.SourceFile, &r.SourceSection, &r.ImportedAt, &r.Predicate, &r.Count); err != nil {
			return nil, nil, err
		}

		content := string(r.Content)
		captureLike := isAutoCapturePath(r.SourceFile) || isTranscriptLike(content)
		if !captureLike {
			continue
		}

		norm := normalizePredicate(r.Predicate)
		if _, ok := noisy[norm]; !ok {
			continue
		}

		mem := memMap[r.MemoryID]
		if mem == nil {
			mem = &memoryRow{
				ID:            r.MemoryID,
				Content:       content,
				SourceFile:    r.SourceFile,
				SourceSection: r.SourceSection,
				ImportedAt:    r.ImportedAt,
			}
			memMap[r.MemoryID] = mem
		}
		mem.NoisyFacts += r.Count
		sourceNoise[r.SourceFile] += r.Count
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	all := make([]memoryRow, 0, len(memMap))
	for _, m := range memMap {
		all = append(all, *m)
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].ImportedAt == all[j].ImportedAt {
			return all[i].ID > all[j].ID
		}
		return all[i].ImportedAt > all[j].ImportedAt
	})

	if offset >= len(all) {
		return []memoryRow{}, []sourceCount{}, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	selected := all[offset:end]

	top := make([]sourceCount, 0, len(sourceNoise))
	for src, c := range sourceNoise {
		top = append(top, sourceCount{Source: src, NoisyFacts: c})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].NoisyFacts > top[j].NoisyFacts })
	if len(top) > 20 {
		top = top[:20]
	}

	return selected, top, nil
}

func calcMetrics(ctx context.Context, db *sql.DB, ids []int64) (metrics, error) {
	m := metrics{FactsByType: map[string]int64{}}

	args := []any{}
	where := ""
	if len(ids) > 0 {
		for _, id := range ids {
			args = append(args, id)
		}
		where = fmt.Sprintf("WHERE memory_id IN (%s)", placeholders(len(ids)))
	}

	qTotal := fmt.Sprintf(`SELECT COUNT(*), COUNT(DISTINCT memory_id) FROM facts %s`, where)
	if err := db.QueryRowContext(ctx, qTotal, args...).Scan(&m.FactsTotal, &m.DistinctMemories); err != nil {
		return m, err
	}

	qType := fmt.Sprintf(`SELECT fact_type, COUNT(*) FROM facts %s GROUP BY fact_type`, where)
	rows, err := db.QueryContext(ctx, qType, args...)
	if err != nil {
		return m, err
	}
	for rows.Next() {
		var t string
		var c int64
		if err := rows.Scan(&t, &c); err != nil {
			rows.Close()
			return m, err
		}
		m.FactsByType[t] = c
		if t == "kv" {
			m.KVFacts = c
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return m, err
	}
	rows.Close()

	qPred := fmt.Sprintf(`SELECT predicate, COUNT(*) FROM facts %s AND fact_type='kv' GROUP BY predicate`, func() string {
		if where == "" {
			return "WHERE 1=1"
		}
		return where
	}())
	rows, err = db.QueryContext(ctx, qPred, args...)
	if err != nil {
		return m, err
	}
	noisy := noisyPredicateSet()
	for rows.Next() {
		var p string
		var c int64
		if err := rows.Scan(&p, &c); err != nil {
			rows.Close()
			return m, err
		}
		if _, ok := noisy[normalizePredicate(p)]; ok {
			m.NoisyKVFacts += c
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return m, err
	}
	rows.Close()

	if m.KVFacts > 0 {
		m.NoisyKVPct = (float64(m.NoisyKVFacts) / float64(m.KVFacts)) * 100.0
	}

	return m, nil
}

func backupDB(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.ReadFrom(in); err != nil {
		return err
	}
	return out.Sync()
}

func main() {
	dbPath := flag.String("db", filepath.Join(os.Getenv("HOME"), ".cortex", "cortex.db"), "Path to cortex sqlite db")
	since := flag.String("since", "", "ISO timestamp lower-bound for imported_at (optional)")
	limit := flag.Int("limit", 250, "Max transcript-like noisy memories to reprocess")
	offset := flag.Int("offset", 0, "Offset into ordered candidate memories")
	dryRun := flag.Bool("dry-run", false, "Only report counts, do not mutate")
	backupPath := flag.String("backup", "", "Backup path before write mode")
	reportPath := flag.String("report", "", "Optional path to write JSON report")
	flag.Parse()

	ctx := context.Background()
	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	candidates, topSources, err := collectCandidates(ctx, db, *since, *limit, *offset)
	if err != nil {
		panic(err)
	}

	ids := make([]int64, 0, len(candidates))
	selectedNoisy := int64(0)
	for _, c := range candidates {
		ids = append(ids, c.ID)
		selectedNoisy += c.NoisyFacts
	}

	rep := report{
		DBPath:        *dbPath,
		GeneratedAt:   time.Now().UTC(),
		Mode:          map[bool]string{true: "dry-run", false: "write"}[*dryRun],
		Limit:         *limit,
		Offset:        *offset,
		Since:         *since,
		Selected:      len(candidates),
		SelectedNoisy: selectedNoisy,
		TopSources:    topSources,
	}

	rep.GlobalBefore, err = calcMetrics(ctx, db, nil)
	if err != nil {
		panic(err)
	}
	rep.SubsetBefore, err = calcMetrics(ctx, db, ids)
	if err != nil {
		panic(err)
	}

	if !*dryRun {
		if *backupPath != "" {
			if err := backupDB(*dbPath, *backupPath); err != nil {
				panic(fmt.Errorf("backup failed: %w", err))
			}
			rep.BackupPath = *backupPath
		}

		pipeline := extract.NewPipeline()
		now := time.Now().UTC().Format("2006-01-02 15:04:05")

		for _, mem := range candidates {
			meta := map[string]string{"source_file": mem.SourceFile}
			if strings.HasSuffix(strings.ToLower(mem.SourceFile), ".md") {
				meta["format"] = "markdown"
			}
			if strings.TrimSpace(mem.SourceSection) != "" {
				meta["source_section"] = mem.SourceSection
			}

			extracted, err := pipeline.Extract(ctx, mem.Content, meta)
			if err != nil {
				rep.Failed++
				rep.Errors = append(rep.Errors, fmt.Sprintf("memory %d extract error: %v", mem.ID, err))
				continue
			}

			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				rep.Failed++
				rep.Errors = append(rep.Errors, fmt.Sprintf("memory %d tx begin error: %v", mem.ID, err))
				continue
			}

			res, err := tx.ExecContext(ctx, `DELETE FROM facts WHERE memory_id = ?`, mem.ID)
			if err != nil {
				_ = tx.Rollback()
				rep.Failed++
				rep.Errors = append(rep.Errors, fmt.Sprintf("memory %d delete error: %v", mem.ID, err))
				continue
			}
			deleted, _ := res.RowsAffected()
			rep.DeletedFacts += deleted

			for _, f := range extracted {
				_, err := tx.ExecContext(ctx, `
INSERT INTO facts (memory_id, subject, predicate, object, fact_type, confidence, decay_rate, last_reinforced, source_quote, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, mem.ID, f.Subject, f.Predicate, f.Object, f.FactType, f.Confidence, f.DecayRate, now, f.SourceQuote, now)
				if err != nil {
					_ = tx.Rollback()
					rep.Failed++
					rep.Errors = append(rep.Errors, fmt.Sprintf("memory %d insert error: %v", mem.ID, err))
					goto nextMemory
				}
				rep.InsertedFacts++
			}

			if err := tx.Commit(); err != nil {
				rep.Failed++
				rep.Errors = append(rep.Errors, fmt.Sprintf("memory %d commit error: %v", mem.ID, err))
				continue
			}
			rep.Processed++

		nextMemory:
		}
	}

	rep.GlobalAfter, err = calcMetrics(ctx, db, nil)
	if err != nil {
		panic(err)
	}
	rep.SubsetAfter, err = calcMetrics(ctx, db, ids)
	if err != nil {
		panic(err)
	}

	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, out, 0o644); err != nil {
			panic(err)
		}
	}
}
