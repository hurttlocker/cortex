package store

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestClusterRebuildAndList(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	seedClusterFacts(t, ctx, s)

	rebuild, err := s.RebuildClusters(ctx)
	if err != nil {
		t.Fatalf("RebuildClusters: %v", err)
	}
	if rebuild.Clusters < 2 {
		t.Fatalf("expected >=2 clusters, got %d", rebuild.Clusters)
	}
	if rebuild.FactAssignments == 0 {
		t.Fatal("expected fact assignments during rebuild")
	}

	clusters, err := s.ListClusters(ctx)
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}
	if len(clusters) < 2 {
		t.Fatalf("expected >=2 clusters, got %d", len(clusters))
	}

	trading, err := s.FindClusterByName(ctx, "trading")
	if err != nil {
		t.Fatalf("FindClusterByName(trading): %v", err)
	}
	if trading == nil {
		t.Fatal("expected to find trading cluster")
	}
	if trading.Name == "" {
		t.Fatal("expected non-empty cluster name")
	}
	if len(trading.TopSubjects) == 0 {
		t.Fatal("expected top subjects for trading cluster")
	}

	unclustered, err := s.CountUnclusteredFacts(ctx)
	if err != nil {
		t.Fatalf("CountUnclusteredFacts: %v", err)
	}
	if unclustered != 0 {
		t.Fatalf("expected 0 unclustered seeded facts, got %d", unclustered)
	}
}

func TestClusterIncrementalAssignsNewFacts(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	seedClusterFacts(t, ctx, s)
	if _, err := s.RebuildClusters(ctx); err != nil {
		t.Fatalf("RebuildClusters: %v", err)
	}

	memID := addMemory(t, ctx, s, "incremental cluster memory")
	newFactIDs := []int64{
		addFact(t, ctx, s, memID, "trading", "adds", "risk budget", 0.84),
		addFact(t, ctx, s, memID, "orb strategy", "uses", "scanner", 0.79),
	}

	result, err := s.UpdateClusters(ctx, newFactIDs)
	if err != nil {
		t.Fatalf("UpdateClusters: %v", err)
	}
	if result.Rebuilt {
		t.Fatalf("expected incremental update, got rebuild")
	}
	if result.FactsAssigned < len(newFactIDs) {
		t.Fatalf("expected >=%d assigned facts, got %d", len(newFactIDs), result.FactsAssigned)
	}

	for _, factID := range newFactIDs {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fact_clusters WHERE fact_id = ?`, factID).Scan(&count); err != nil {
			t.Fatalf("count assignments for fact %d: %v", factID, err)
		}
		if count == 0 {
			t.Fatalf("expected fact %d to be assigned to a cluster", factID)
		}
	}
}

func TestClusterUpdateRebuildsOnSubjectGrowth(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	memID := addMemory(t, ctx, s, "base growth memory")
	for i := 0; i < 10; i++ {
		addFact(t, ctx, s, memID, fmt.Sprintf("base-%d", i), "state", "active", 0.7)
	}

	if _, err := s.RebuildClusters(ctx); err != nil {
		t.Fatalf("RebuildClusters: %v", err)
	}

	newMemID := addMemory(t, ctx, s, "new growth memory")
	newFactIDs := []int64{
		addFact(t, ctx, s, newMemID, "novel-a", "status", "new", 0.66),
		addFact(t, ctx, s, newMemID, "novel-b", "status", "new", 0.67),
	}

	result, err := s.UpdateClusters(ctx, newFactIDs)
	if err != nil {
		t.Fatalf("UpdateClusters: %v", err)
	}
	if !result.Rebuilt {
		t.Fatalf("expected rebuild when new subject ratio exceeds threshold")
	}
}

func TestClusterRebuildMatchesIncremental(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	seedClusterFacts(t, ctx, s)
	if _, err := s.RebuildClusters(ctx); err != nil {
		t.Fatalf("initial RebuildClusters: %v", err)
	}

	memID := addMemory(t, ctx, s, "consistency memory")
	newFactIDs := []int64{
		addFact(t, ctx, s, memID, "trading", "status", "stable", 0.86),
		addFact(t, ctx, s, memID, "spear", "status", "live", 0.81),
	}

	inc, err := s.UpdateClusters(ctx, newFactIDs)
	if err != nil {
		t.Fatalf("UpdateClusters: %v", err)
	}
	if inc.Rebuilt {
		t.Fatalf("expected incremental path for existing subjects")
	}

	before, err := factClusterNameMap(ctx, s)
	if err != nil {
		t.Fatalf("factClusterNameMap before rebuild: %v", err)
	}

	if _, err := s.RebuildClusters(ctx); err != nil {
		t.Fatalf("RebuildClusters after incremental: %v", err)
	}

	after, err := factClusterNameMap(ctx, s)
	if err != nil {
		t.Fatalf("factClusterNameMap after rebuild: %v", err)
	}

	if !reflect.DeepEqual(before, after) {
		t.Fatalf("expected incremental assignments to match rebuild\nbefore=%v\nafter=%v", before, after)
	}
}

func TestGetClusterDetail(t *testing.T) {
	s := newTestStore(t).(*SQLiteStore)
	ctx := context.Background()

	seedClusterFacts(t, ctx, s)
	if _, err := s.RebuildClusters(ctx); err != nil {
		t.Fatalf("RebuildClusters: %v", err)
	}

	cluster, err := s.FindClusterByName(ctx, "trading")
	if err != nil {
		t.Fatalf("FindClusterByName: %v", err)
	}
	if cluster == nil {
		t.Fatal("expected trading cluster")
	}

	detail, err := s.GetClusterDetail(ctx, cluster.ID, 500)
	if err != nil {
		t.Fatalf("GetClusterDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected cluster detail")
	}
	if detail.Cluster.ID != cluster.ID {
		t.Fatalf("detail cluster id=%d, want %d", detail.Cluster.ID, cluster.ID)
	}
	if len(detail.Facts) == 0 {
		t.Fatal("expected cluster detail to include facts")
	}
}

func seedClusterFacts(t *testing.T, ctx context.Context, s *SQLiteStore) {
	t.Helper()

	memTradingA := addMemory(t, ctx, s, "trading memory A")
	addFact(t, ctx, s, memTradingA, "trading", "strategy", "orb", 0.93)
	addFact(t, ctx, s, memTradingA, "orb strategy", "runs_on", "alpaca", 0.88)
	addFact(t, ctx, s, memTradingA, "alpaca", "supports", "paper", 0.8)

	memTradingB := addMemory(t, ctx, s, "trading memory B")
	addFact(t, ctx, s, memTradingB, "trading", "uses", "options scanner", 0.91)
	addFact(t, ctx, s, memTradingB, "orb strategy", "entry", "breakout", 0.86)
	addFact(t, ctx, s, memTradingB, "options", "market", "qqq", 0.78)

	memSpearA := addMemory(t, ctx, s, "spear memory A")
	addFact(t, ctx, s, memSpearA, "spear", "uses", "hha", 0.81)
	addFact(t, ctx, s, memSpearA, "paypal", "linked_to", "spear", 0.74)

	memSpearB := addMemory(t, ctx, s, "spear memory B")
	addFact(t, ctx, s, memSpearB, "spear", "manages", "rustdesk fleet", 0.82)
	addFact(t, ctx, s, memSpearB, "rustdesk", "fleet", "managed", 0.75)
}

func addMemory(t *testing.T, ctx context.Context, s *SQLiteStore, content string) int64 {
	t.Helper()
	id, err := s.AddMemory(ctx, &Memory{Content: content, SourceFile: "cluster_test.md"})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	return id
}

func addFact(t *testing.T, ctx context.Context, s *SQLiteStore, memoryID int64, subject, predicate, object string, confidence float64) int64 {
	t.Helper()
	id, err := s.AddFact(ctx, &Fact{
		MemoryID:   memoryID,
		Subject:    subject,
		Predicate:  predicate,
		Object:     object,
		FactType:   "kv",
		Confidence: confidence,
	})
	if err != nil {
		t.Fatalf("add fact (%s %s %s): %v", subject, predicate, object, err)
	}
	return id
}

func factClusterNameMap(ctx context.Context, s *SQLiteStore) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT fc.fact_id, c.name
		 FROM fact_clusters fc
		 JOIN clusters c ON c.id = fc.cluster_id
		 ORDER BY fc.fact_id ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]string)
	for rows.Next() {
		var factID int64
		var clusterName string
		if err := rows.Scan(&factID, &clusterName); err != nil {
			return nil, err
		}
		result[factID] = strings.ToLower(strings.TrimSpace(clusterName))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Normalize map values in deterministic order for stable diffs in test failures.
	keys := make([]int64, 0, len(result))
	for k := range result {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	normalized := make(map[int64]string, len(result))
	for _, k := range keys {
		normalized[k] = result[k]
	}
	return normalized, nil
}
