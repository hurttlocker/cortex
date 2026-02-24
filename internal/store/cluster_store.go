package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hurttlocker/cortex/internal/extract"
)

const clusterRebuildSubjectDeltaThreshold = 0.10

var clusterPalette = []string{
	"#8b5cf6", "#06b6d4", "#22c55e", "#f59e0b", "#ef4444",
	"#14b8a6", "#eab308", "#3b82f6", "#d946ef", "#f97316",
}

// Cluster describes a topic cluster and its aggregate metrics.
type Cluster struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Aliases       []string `json:"aliases"`
	Cohesion      float64  `json:"cohesion"`
	FactCount     int      `json:"fact_count"`
	AvgConfidence float64  `json:"avg_confidence"`
	Color         string   `json:"color"`
	TopSubjects   []string `json:"top_subjects"`
}

// ClusterDetail is a cluster with all currently assigned facts.
type ClusterDetail struct {
	Cluster Cluster `json:"cluster"`
	Facts   []*Fact `json:"facts"`
}

// ClusterRebuildResult summarizes a full cluster rebuild.
type ClusterRebuildResult struct {
	Clusters         int `json:"clusters"`
	FactAssignments  int `json:"fact_assignments"`
	TotalSubjects    int `json:"total_subjects"`
	UnclusteredFacts int `json:"unclustered_facts"`
}

// ClusterUpdateResult summarizes an incremental cluster update.
type ClusterUpdateResult struct {
	Rebuilt       bool `json:"rebuilt"`
	Clusters      int  `json:"clusters"`
	FactsAssigned int  `json:"facts_assigned"`
	NewSubjects   int  `json:"new_subjects"`
	TotalSubjects int  `json:"total_subjects"`
}

// ClusterTablesAvailable returns true when cluster tables exist.
func (s *SQLiteStore) ClusterTablesAvailable(ctx context.Context) bool {
	var clusters int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='clusters'`).Scan(&clusters); err != nil {
		return false
	}
	var factClusters int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='fact_clusters'`).Scan(&factClusters); err != nil {
		return false
	}
	return clusters > 0 && factClusters > 0
}

// RebuildClusters fully recomputes topic clusters from active facts.
func (s *SQLiteStore) RebuildClusters(ctx context.Context) (*ClusterRebuildResult, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, memory_id, COALESCE(subject, ''), confidence
		 FROM facts
		 WHERE (superseded_by IS NULL OR superseded_by = 0)`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying facts for cluster rebuild: %w", err)
	}
	defer rows.Close()

	input := make([]extract.ClusterFact, 0, 256)
	for rows.Next() {
		var f extract.ClusterFact
		if err := rows.Scan(&f.ID, &f.MemoryID, &f.Subject, &f.Confidence); err != nil {
			return nil, fmt.Errorf("scanning fact for cluster rebuild: %w", err)
		}
		input = append(input, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating facts for cluster rebuild: %w", err)
	}

	build := extract.BuildTopicClusters(input)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin cluster rebuild transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM fact_clusters`); err != nil {
		return nil, fmt.Errorf("clearing fact_clusters: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM clusters`); err != nil {
		return nil, fmt.Errorf("clearing clusters: %w", err)
	}

	assignments := 0
	for _, cluster := range build.Clusters {
		aliasesJSON, err := json.Marshal(cluster.Aliases)
		if err != nil {
			return nil, fmt.Errorf("encoding cluster aliases: %w", err)
		}

		res, err := tx.ExecContext(ctx,
			`INSERT INTO clusters (name, aliases, cohesion, fact_count, avg_confidence, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			cluster.Name,
			string(aliasesJSON),
			cluster.Cohesion,
			cluster.FactCount,
			cluster.AvgConfidence,
		)
		if err != nil {
			return nil, fmt.Errorf("inserting cluster %q: %w", cluster.Name, err)
		}

		clusterID, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("reading cluster insert id: %w", err)
		}

		for _, factID := range cluster.FactIDs {
			insertRes, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO fact_clusters (fact_id, cluster_id, relevance)
				 VALUES (?, ?, ?)`,
				factID,
				clusterID,
				1.0,
			)
			if err != nil {
				return nil, fmt.Errorf("assigning fact %d to cluster %d: %w", factID, clusterID, err)
			}
			rowsAffected, _ := insertRes.RowsAffected()
			assignments += int(rowsAffected)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit cluster rebuild: %w", err)
	}

	return &ClusterRebuildResult{
		Clusters:         len(build.Clusters),
		FactAssignments:  assignments,
		TotalSubjects:    build.TotalSubjects,
		UnclusteredFacts: len(build.UnclusteredFactIDs),
	}, nil
}

// UpdateClusters incrementally assigns new facts when possible and falls back to full rebuild
// when novel subject growth suggests topology drift.
func (s *SQLiteStore) UpdateClusters(ctx context.Context, newFactIDs []int64) (*ClusterUpdateResult, error) {
	ids := uniquePositiveInt64(newFactIDs)
	if len(ids) == 0 {
		clusterCount, _ := s.countClusters(ctx)
		return &ClusterUpdateResult{Clusters: clusterCount}, nil
	}

	totalSubjects, err := s.countDistinctActiveSubjects(ctx)
	if err != nil {
		return nil, err
	}
	newSubjects, err := s.countDistinctNewSubjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	clusterCount, err := s.countClusters(ctx)
	if err != nil {
		return nil, err
	}

	existingSubjects := totalSubjects - newSubjects
	shouldRebuild := clusterCount == 0 || existingSubjects <= 0
	if !shouldRebuild {
		ratio := float64(newSubjects) / float64(existingSubjects)
		shouldRebuild = ratio > clusterRebuildSubjectDeltaThreshold
	}

	if shouldRebuild {
		rebuild, err := s.RebuildClusters(ctx)
		if err != nil {
			return nil, err
		}
		return &ClusterUpdateResult{
			Rebuilt:       true,
			Clusters:      rebuild.Clusters,
			FactsAssigned: rebuild.FactAssignments,
			NewSubjects:   newSubjects,
			TotalSubjects: totalSubjects,
		}, nil
	}

	assigned, err := s.assignFactsToExistingClusters(ctx, ids)
	if err != nil {
		return nil, err
	}
	if err := s.refreshClusterRollups(ctx); err != nil {
		return nil, err
	}

	clusters, err := s.ListClusters(ctx)
	if err != nil {
		return nil, err
	}

	return &ClusterUpdateResult{
		Rebuilt:       false,
		Clusters:      len(clusters),
		FactsAssigned: assigned,
		NewSubjects:   newSubjects,
		TotalSubjects: totalSubjects,
	}, nil
}

// ListClusters returns all clusters ordered by size/cohesion.
func (s *SQLiteStore) ListClusters(ctx context.Context) ([]Cluster, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, COALESCE(aliases, '[]'), cohesion, fact_count, avg_confidence
		 FROM clusters
		 ORDER BY fact_count DESC, cohesion DESC, name ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing clusters: %w", err)
	}
	defer rows.Close()

	clusters := make([]Cluster, 0, 64)
	for rows.Next() {
		var c Cluster
		var aliasesRaw string
		if err := rows.Scan(&c.ID, &c.Name, &aliasesRaw, &c.Cohesion, &c.FactCount, &c.AvgConfidence); err != nil {
			return nil, fmt.Errorf("scanning cluster row: %w", err)
		}
		c.Aliases = parseAliasesJSON(aliasesRaw)
		clusters = append(clusters, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating clusters: %w", err)
	}
	for i := range clusters {
		clusters[i].TopSubjects, _ = s.loadTopSubjectsForCluster(ctx, clusters[i].ID, 5)
		clusters[i].Color = colorForCluster(clusters[i].ID, clusters[i].Name)
	}
	return clusters, nil
}

// GetClusterByID returns a cluster summary by ID, or nil when not found.
func (s *SQLiteStore) GetClusterByID(ctx context.Context, id int64) (*Cluster, error) {
	var c Cluster
	var aliasesRaw string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, COALESCE(aliases, '[]'), cohesion, fact_count, avg_confidence
		 FROM clusters
		 WHERE id = ?`,
		id,
	).Scan(&c.ID, &c.Name, &aliasesRaw, &c.Cohesion, &c.FactCount, &c.AvgConfidence)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting cluster %d: %w", id, err)
	}
	c.Aliases = parseAliasesJSON(aliasesRaw)
	c.TopSubjects, _ = s.loadTopSubjectsForCluster(ctx, c.ID, 5)
	c.Color = colorForCluster(c.ID, c.Name)
	return &c, nil
}

// FindClusterByName finds a cluster by name or alias (case-insensitive).
func (s *SQLiteStore) FindClusterByName(ctx context.Context, name string) (*Cluster, error) {
	needle := strings.TrimSpace(name)
	if needle == "" {
		return nil, nil
	}

	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM clusters WHERE LOWER(name) = LOWER(?) LIMIT 1`,
		needle,
	).Scan(&id)
	if err == nil {
		return s.GetClusterByID(ctx, id)
	}
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("finding cluster %q: %w", needle, err)
	}

	clusters, err := s.ListClusters(ctx)
	if err != nil {
		return nil, err
	}
	needleLower := strings.ToLower(needle)
	for i := range clusters {
		for _, alias := range clusters[i].Aliases {
			if strings.ToLower(alias) == needleLower {
				return &clusters[i], nil
			}
		}
	}
	return nil, nil
}

// GetClusterDetail returns a cluster and all assigned facts.
func (s *SQLiteStore) GetClusterDetail(ctx context.Context, clusterID int64, limit int) (*ClusterDetail, error) {
	cluster, err := s.GetClusterByID(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if cluster == nil {
		return nil, nil
	}
	facts, err := s.ListClusterFacts(ctx, clusterID, limit)
	if err != nil {
		return nil, err
	}
	return &ClusterDetail{Cluster: *cluster, Facts: facts}, nil
}

// ListClusterFacts returns all facts assigned to a cluster.
func (s *SQLiteStore) ListClusterFacts(ctx context.Context, clusterID int64, limit int) ([]*Fact, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.id, f.memory_id, f.subject, f.predicate, f.object, f.fact_type,
		        f.confidence, f.decay_rate, f.last_reinforced, f.source_quote,
		        f.created_at, f.superseded_by, COALESCE(f.agent_id, '')
		 FROM facts f
		 JOIN fact_clusters fc ON fc.fact_id = f.id
		 WHERE fc.cluster_id = ?
		 ORDER BY f.confidence DESC, f.id DESC
		 LIMIT ?`,
		clusterID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing cluster facts: %w", err)
	}
	defer rows.Close()

	facts := make([]*Fact, 0, limit)
	for rows.Next() {
		f := &Fact{}
		var supersededBy sql.NullInt64
		if err := rows.Scan(
			&f.ID,
			&f.MemoryID,
			&f.Subject,
			&f.Predicate,
			&f.Object,
			&f.FactType,
			&f.Confidence,
			&f.DecayRate,
			&f.LastReinforced,
			&f.SourceQuote,
			&f.CreatedAt,
			&supersededBy,
			&f.AgentID,
		); err != nil {
			return nil, fmt.Errorf("scanning cluster fact: %w", err)
		}
		if supersededBy.Valid {
			v := supersededBy.Int64
			f.SupersededBy = &v
		}
		facts = append(facts, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating cluster facts: %w", err)
	}
	return facts, nil
}

// CountUnclusteredFacts returns active facts with no cluster assignment.
func (s *SQLiteStore) CountUnclusteredFacts(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		 FROM facts f
		 WHERE (f.superseded_by IS NULL OR f.superseded_by = 0)
		   AND NOT EXISTS (SELECT 1 FROM fact_clusters fc WHERE fc.fact_id = f.id)`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting unclustered facts: %w", err)
	}
	return count, nil
}

// CountActiveFacts returns active (non-superseded) fact count.
func (s *SQLiteStore) CountActiveFacts(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM facts WHERE superseded_by IS NULL OR superseded_by = 0`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting active facts: %w", err)
	}
	return count, nil
}

func (s *SQLiteStore) countClusters(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clusters`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting clusters: %w", err)
	}
	return count, nil
}

func (s *SQLiteStore) countDistinctActiveSubjects(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT LOWER(TRIM(subject)))
		 FROM facts
		 WHERE (superseded_by IS NULL OR superseded_by = 0)
		   AND TRIM(COALESCE(subject, '')) != ''`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting active subjects: %w", err)
	}
	return count, nil
}

func (s *SQLiteStore) countDistinctNewSubjects(ctx context.Context, factIDs []int64) (int, error) {
	if len(factIDs) == 0 {
		return 0, nil
	}

	placeholders := make([]string, len(factIDs))
	args := make([]interface{}, len(factIDs))
	for i, id := range factIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT COUNT(DISTINCT LOWER(TRIM(f.subject)))
		 FROM facts f
		 WHERE f.id IN (%s)
		   AND (f.superseded_by IS NULL OR f.superseded_by = 0)
		   AND TRIM(COALESCE(f.subject, '')) != ''
		   AND NOT EXISTS (
		     SELECT 1
		     FROM fact_clusters fc
		     JOIN facts existing ON existing.id = fc.fact_id
		     WHERE LOWER(TRIM(existing.subject)) = LOWER(TRIM(f.subject))
		   )`,
		strings.Join(placeholders, ","),
	)

	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting new subjects: %w", err)
	}
	return count, nil
}

func (s *SQLiteStore) assignFactsToExistingClusters(ctx context.Context, factIDs []int64) (int, error) {
	if len(factIDs) == 0 {
		return 0, nil
	}

	placeholders := make([]string, len(factIDs))
	args := make([]interface{}, len(factIDs))
	for i, id := range factIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT id, memory_id, TRIM(COALESCE(subject, ''))
		 FROM facts
		 WHERE id IN (%s)
		   AND (superseded_by IS NULL OR superseded_by = 0)`,
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("querying new facts for incremental clustering: %w", err)
	}
	type clusterCandidate struct {
		factID   int64
		memoryID int64
		subject  string
	}
	candidates := make([]clusterCandidate, 0, len(factIDs))
	for rows.Next() {
		var factID, memoryID int64
		var subject string
		if err := rows.Scan(&factID, &memoryID, &subject); err != nil {
			return 0, fmt.Errorf("scanning new fact for incremental clustering: %w", err)
		}
		subject = strings.TrimSpace(subject)
		if subject == "" {
			continue
		}
		candidates = append(candidates, clusterCandidate{factID: factID, memoryID: memoryID, subject: subject})
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterating incremental clustering facts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("closing incremental clustering rows: %w", err)
	}

	assigned := 0
	for _, candidate := range candidates {
		clusterID, err := s.lookupClusterForSubject(ctx, candidate.subject)
		if err != nil {
			return 0, err
		}
		if clusterID == 0 {
			clusterID, err = s.lookupClusterForMemory(ctx, candidate.memoryID)
			if err != nil {
				return 0, err
			}
		}
		if clusterID == 0 {
			continue
		}

		res, err := s.db.ExecContext(ctx,
			`INSERT OR IGNORE INTO fact_clusters (fact_id, cluster_id, relevance)
			 VALUES (?, ?, ?)`,
			candidate.factID,
			clusterID,
			1.0,
		)
		if err != nil {
			return 0, fmt.Errorf("assigning fact %d to cluster %d: %w", candidate.factID, clusterID, err)
		}
		rowsAffected, _ := res.RowsAffected()
		assigned += int(rowsAffected)
	}

	return assigned, nil
}

func (s *SQLiteStore) lookupClusterForSubject(ctx context.Context, subject string) (int64, error) {
	var clusterID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT fc.cluster_id
		 FROM fact_clusters fc
		 JOIN facts f ON f.id = fc.fact_id
		 WHERE LOWER(TRIM(f.subject)) = LOWER(TRIM(?))
		 GROUP BY fc.cluster_id
		 ORDER BY COUNT(*) DESC, fc.cluster_id ASC
		 LIMIT 1`,
		subject,
	).Scan(&clusterID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("looking up cluster by subject %q: %w", subject, err)
	}
	return clusterID, nil
}

func (s *SQLiteStore) lookupClusterForMemory(ctx context.Context, memoryID int64) (int64, error) {
	if memoryID <= 0 {
		return 0, nil
	}
	var clusterID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT fc.cluster_id
		 FROM fact_clusters fc
		 JOIN facts f ON f.id = fc.fact_id
		 WHERE f.memory_id = ?
		 GROUP BY fc.cluster_id
		 ORDER BY COUNT(*) DESC, fc.cluster_id ASC
		 LIMIT 1`,
		memoryID,
	).Scan(&clusterID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("looking up cluster by memory %d: %w", memoryID, err)
	}
	return clusterID, nil
}

func (s *SQLiteStore) refreshClusterRollups(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM clusters ORDER BY id ASC`)
	if err != nil {
		return fmt.Errorf("querying cluster ids for rollup refresh: %w", err)
	}
	defer rows.Close()

	clusterIDs := make([]int64, 0, 64)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scanning cluster id: %w", err)
		}
		clusterIDs = append(clusterIDs, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating cluster ids: %w", err)
	}

	for _, clusterID := range clusterIDs {
		rollup, err := s.computeClusterRollup(ctx, clusterID)
		if err != nil {
			return err
		}

		aliasesJSON, err := json.Marshal(rollup.aliases)
		if err != nil {
			return fmt.Errorf("encoding rollup aliases: %w", err)
		}

		if _, err := s.db.ExecContext(ctx,
			`UPDATE clusters
			 SET name = ?, aliases = ?, cohesion = ?, fact_count = ?, avg_confidence = ?, updated_at = CURRENT_TIMESTAMP
			 WHERE id = ?`,
			rollup.name,
			string(aliasesJSON),
			rollup.cohesion,
			rollup.factCount,
			rollup.avgConfidence,
			clusterID,
		); err != nil {
			return fmt.Errorf("updating rollup for cluster %d: %w", clusterID, err)
		}
	}
	return nil
}

type clusterRollup struct {
	name          string
	aliases       []string
	factCount     int
	avgConfidence float64
	cohesion      float64
}

func (s *SQLiteStore) computeClusterRollup(ctx context.Context, clusterID int64) (*clusterRollup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.id, f.memory_id, COALESCE(f.subject, ''), f.confidence
		 FROM fact_clusters fc
		 JOIN facts f ON f.id = fc.fact_id
		 WHERE fc.cluster_id = ?`,
		clusterID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying cluster %d facts for rollup: %w", clusterID, err)
	}
	defer rows.Close()

	subjectDisplay := make(map[string]string)
	subjectFreq := make(map[string]int)
	subjectConfSum := make(map[string]float64)
	memorySubjects := make(map[int64]map[string]struct{})

	factCount := 0
	confSum := 0.0
	for rows.Next() {
		var f extract.ClusterFact
		if err := rows.Scan(&f.ID, &f.MemoryID, &f.Subject, &f.Confidence); err != nil {
			return nil, fmt.Errorf("scanning cluster %d fact for rollup: %w", clusterID, err)
		}
		factCount++
		confSum += f.Confidence

		subjectKey := normalizeSubjectKey(f.Subject)
		if subjectKey == "" {
			continue
		}
		if _, ok := subjectDisplay[subjectKey]; !ok {
			subjectDisplay[subjectKey] = strings.TrimSpace(f.Subject)
		}
		subjectFreq[subjectKey]++
		subjectConfSum[subjectKey] += f.Confidence
		if f.MemoryID > 0 {
			if _, ok := memorySubjects[f.MemoryID]; !ok {
				memorySubjects[f.MemoryID] = make(map[string]struct{})
			}
			memorySubjects[f.MemoryID][subjectKey] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating cluster %d facts for rollup: %w", clusterID, err)
	}

	keys := make([]string, 0, len(subjectDisplay))
	for key := range subjectDisplay {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a := keys[i]
		b := keys[j]
		if subjectFreq[a] != subjectFreq[b] {
			return subjectFreq[a] > subjectFreq[b]
		}
		return strings.ToLower(subjectDisplay[a]) < strings.ToLower(subjectDisplay[b])
	})

	name := ""
	if len(keys) > 0 {
		name = subjectDisplay[keys[0]]
	}
	aliases := make([]string, 0)
	if len(keys) > 1 {
		for _, key := range keys[1:] {
			aliases = append(aliases, subjectDisplay[key])
		}
		if len(aliases) > 8 {
			aliases = aliases[:8]
		}
	}

	cohesion := 1.0
	if len(keys) > 1 {
		edges := buildSubjectEdges(memorySubjects)
		possible := len(keys) * (len(keys) - 1) / 2
		actual := 0
		for i := 0; i < len(keys)-1; i++ {
			for j := i + 1; j < len(keys); j++ {
				if edges[keys[i]][keys[j]] {
					actual++
				}
			}
		}
		if possible > 0 {
			cohesion = float64(actual) / float64(possible)
		}
	}

	avgConf := 0.0
	if factCount > 0 {
		avgConf = confSum / float64(factCount)
	}

	return &clusterRollup{
		name:          name,
		aliases:       aliases,
		factCount:     factCount,
		avgConfidence: avgConf,
		cohesion:      cohesion,
	}, nil
}

func (s *SQLiteStore) loadTopSubjectsForCluster(ctx context.Context, clusterID int64, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT MIN(f.subject) AS subject, COUNT(*) AS cnt
		 FROM facts f
		 JOIN fact_clusters fc ON fc.fact_id = f.id
		 WHERE fc.cluster_id = ?
		   AND TRIM(COALESCE(f.subject, '')) != ''
		 GROUP BY LOWER(TRIM(f.subject))
		 ORDER BY cnt DESC, subject ASC
		 LIMIT ?`,
		clusterID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("loading top subjects for cluster %d: %w", clusterID, err)
	}
	defer rows.Close()

	subjects := make([]string, 0, limit)
	for rows.Next() {
		var subject string
		var count int
		if err := rows.Scan(&subject, &count); err != nil {
			return nil, fmt.Errorf("scanning top subject row: %w", err)
		}
		subjects = append(subjects, subject)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating top subjects: %w", err)
	}
	return subjects, nil
}

func parseAliasesJSON(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	aliases := make([]string, 0)
	if err := json.Unmarshal([]byte(raw), &aliases); err != nil {
		return nil
	}
	for i := range aliases {
		aliases[i] = strings.TrimSpace(aliases[i])
	}
	out := aliases[:0]
	for _, alias := range aliases {
		if alias == "" {
			continue
		}
		out = append(out, alias)
	}
	return out
}

func colorForCluster(id int64, name string) string {
	if len(clusterPalette) == 0 {
		return "#71717a"
	}
	h := int64(0)
	for _, ch := range strings.ToLower(name) {
		h = (h*33 + int64(ch)) % 2147483647
	}
	idx := int((id + h) % int64(len(clusterPalette)))
	if idx < 0 {
		idx = -idx
	}
	return clusterPalette[idx]
}

func uniquePositiveInt64(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, v := range values {
		if v <= 0 {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func normalizeSubjectKey(subject string) string {
	normalized := strings.ToLower(strings.TrimSpace(subject))
	if normalized == "" {
		return ""
	}
	return strings.Join(strings.Fields(normalized), " ")
}

func buildSubjectEdges(memorySubjects map[int64]map[string]struct{}) map[string]map[string]bool {
	edges := make(map[string]map[string]bool)
	for _, subjectSet := range memorySubjects {
		subjects := make([]string, 0, len(subjectSet))
		for subject := range subjectSet {
			subjects = append(subjects, subject)
		}
		sort.Strings(subjects)
		for i := 0; i < len(subjects)-1; i++ {
			for j := i + 1; j < len(subjects); j++ {
				a := subjects[i]
				b := subjects[j]
				if edges[a] == nil {
					edges[a] = make(map[string]bool)
				}
				if edges[b] == nil {
					edges[b] = make(map[string]bool)
				}
				edges[a][b] = true
				edges[b][a] = true
			}
		}
	}
	return edges
}
