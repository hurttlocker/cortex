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
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/extract"
	_ "modernc.org/sqlite"
)

type memoryRow struct {
	ID            int64
	Content       string
	SourceFile    string
	SourceSection string
	ImportedAt    string
}

type metrics struct {
	FactsTotal   int64            `json:"facts_total"`
	FactsByType  map[string]int64 `json:"facts_by_type"`
	KVFacts      int64            `json:"kv_facts"`
	NoisyKVFacts int64            `json:"noisy_kv_facts"`
}

type report struct {
	DBPath        string    `json:"db_path"`
	GeneratedAt   time.Time `json:"generated_at"`
	Mode          string    `json:"mode"`
	Limit         int       `json:"limit"`
	Offset        int       `json:"offset"`
	Since         string    `json:"since,omitempty"`
	Selected      int       `json:"selected_memories"`
	BackupPath    string    `json:"backup_path,omitempty"`
	Before        metrics   `json:"before"`
	After         metrics   `json:"after"`
	Processed     int       `json:"processed"`
	Failed        int       `json:"failed"`
	DeletedFacts  int64     `json:"deleted_facts"`
	InsertedFacts int64     `json:"inserted_facts"`
	Errors        []string  `json:"errors,omitempty"`
}

var nonAlphaNumRE = regexp.MustCompile(`[^a-z0-9]+`)

func normalizePredicate(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlphaNumRE.ReplaceAllString(s, "")
	return s
}

func noisyPredicateSet() map[string]struct{} {
	keys := []string{
		"conversationlabel", "groupsubject", "groupchannel", "groupspace",
		"sender", "label", "name", "username", "tag",
		"currenttime", "assistant", "user", "system", "messageid",
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

func collectMemories(ctx context.Context, db *sql.DB, since string, limit, offset int) ([]memoryRow, error) {
	args := []any{}
	query := `
SELECT id, content, source_file, source_section, imported_at
FROM memories
WHERE source_file LIKE '%auto-capture%'
`
	if since != "" {
		query += "  AND imported_at >= ?\n"
		args = append(args, since)
	}
	query += "ORDER BY imported_at DESC\nLIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []memoryRow{}
	for rows.Next() {
		var r memoryRow
		if err := rows.Scan(&r.ID, &r.Content, &r.SourceFile, &r.SourceSection, &r.ImportedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func calcMetrics(ctx context.Context, db *sql.DB, ids []int64) (metrics, error) {
	m := metrics{FactsByType: map[string]int64{}}
	if len(ids) == 0 {
		return m, nil
	}

	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	ph := placeholders(len(ids))

	qTotal := fmt.Sprintf(`SELECT COUNT(*) FROM facts WHERE memory_id IN (%s)`, ph)
	if err := db.QueryRowContext(ctx, qTotal, args...).Scan(&m.FactsTotal); err != nil {
		return m, err
	}

	qType := fmt.Sprintf(`SELECT fact_type, COUNT(*) FROM facts WHERE memory_id IN (%s) GROUP BY fact_type`, ph)
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

	qPred := fmt.Sprintf(`SELECT predicate, COUNT(*) FROM facts WHERE memory_id IN (%s) AND fact_type='kv' GROUP BY predicate`, ph)
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
	limit := flag.Int("limit", 250, "Max auto-capture memories to reprocess")
	offset := flag.Int("offset", 0, "Offset into ordered auto-capture memories")
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

	rows, err := collectMemories(ctx, db, *since, *limit, *offset)
	if err != nil {
		panic(err)
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}

	rep := report{
		DBPath:      *dbPath,
		GeneratedAt: time.Now().UTC(),
		Mode:        map[bool]string{true: "dry-run", false: "write"}[*dryRun],
		Limit:       *limit,
		Offset:      *offset,
		Since:       *since,
		Selected:    len(rows),
	}

	rep.Before, err = calcMetrics(ctx, db, ids)
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

		for _, mem := range rows {
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

	rep.After, err = calcMetrics(ctx, db, ids)
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
