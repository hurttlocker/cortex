package extract

import (
	"math"
	"strings"
	"testing"
)

func TestClusterDetection(t *testing.T) {
	facts := []ClusterFact{
		{ID: 1, MemoryID: 10, Subject: "trading", Confidence: 0.95},
		{ID: 2, MemoryID: 10, Subject: "orb strategy", Confidence: 0.88},
		{ID: 3, MemoryID: 10, Subject: "alpaca", Confidence: 0.8},
		{ID: 4, MemoryID: 11, Subject: "trading", Confidence: 0.93},
		{ID: 5, MemoryID: 11, Subject: "orb strategy", Confidence: 0.87},
		{ID: 6, MemoryID: 11, Subject: "options", Confidence: 0.82},
		{ID: 7, MemoryID: 20, Subject: "spear", Confidence: 0.79},
		{ID: 8, MemoryID: 20, Subject: "paypal", Confidence: 0.7},
		{ID: 9, MemoryID: 21, Subject: "spear", Confidence: 0.81},
		{ID: 10, MemoryID: 21, Subject: "rustdesk", Confidence: 0.68},
	}

	result := BuildTopicClusters(facts)
	if len(result.Clusters) < 2 {
		t.Fatalf("expected at least 2 clusters, got %d", len(result.Clusters))
	}

	tradingCluster := findClusterContaining(result.Clusters, "trading")
	if tradingCluster == nil {
		t.Fatal("expected trading cluster")
	}
	if !hasAllSubjects(*tradingCluster, "trading", "orb strategy", "alpaca", "options") {
		t.Fatalf("trading cluster missing expected subjects: %+v", tradingCluster.Subjects)
	}

	spearCluster := findClusterContaining(result.Clusters, "spear")
	if spearCluster == nil {
		t.Fatal("expected spear cluster")
	}
	if !hasAllSubjects(*spearCluster, "spear", "paypal", "rustdesk") {
		t.Fatalf("spear cluster missing expected subjects: %+v", spearCluster.Subjects)
	}
}

func TestClusterCohesionScore(t *testing.T) {
	facts := []ClusterFact{
		// Dense component: full triangle, repeated twice.
		{ID: 1, MemoryID: 1, Subject: "dense-a", Confidence: 0.9},
		{ID: 2, MemoryID: 1, Subject: "dense-b", Confidence: 0.9},
		{ID: 3, MemoryID: 1, Subject: "dense-c", Confidence: 0.9},
		{ID: 4, MemoryID: 2, Subject: "dense-a", Confidence: 0.9},
		{ID: 5, MemoryID: 2, Subject: "dense-b", Confidence: 0.9},
		{ID: 6, MemoryID: 2, Subject: "dense-c", Confidence: 0.9},

		// Sparse component: a 5-node chain.
		{ID: 7, MemoryID: 10, Subject: "s1", Confidence: 0.7},
		{ID: 8, MemoryID: 10, Subject: "s2", Confidence: 0.7},
		{ID: 9, MemoryID: 11, Subject: "s2", Confidence: 0.7},
		{ID: 10, MemoryID: 11, Subject: "s3", Confidence: 0.7},
		{ID: 11, MemoryID: 12, Subject: "s3", Confidence: 0.7},
		{ID: 12, MemoryID: 12, Subject: "s4", Confidence: 0.7},
		{ID: 13, MemoryID: 13, Subject: "s4", Confidence: 0.7},
		{ID: 14, MemoryID: 13, Subject: "s5", Confidence: 0.7},
	}

	result := BuildTopicClusters(facts)

	dense := findClusterContaining(result.Clusters, "dense-a")
	if dense == nil {
		t.Fatal("expected dense cluster")
	}
	if math.Abs(dense.Cohesion-1.0) > 0.01 {
		t.Fatalf("expected dense cohesion ~1.0, got %.3f", dense.Cohesion)
	}

	sparse := findClusterContaining(result.Clusters, "s1")
	if sparse == nil {
		t.Fatal("expected sparse cluster")
	}
	if sparse.Cohesion >= 0.5 {
		t.Fatalf("expected sparse cohesion < 0.5, got %.3f", sparse.Cohesion)
	}
}

func TestClusterNaming(t *testing.T) {
	facts := []ClusterFact{
		{ID: 1, MemoryID: 1, Subject: "trading", Confidence: 0.9},
		{ID: 2, MemoryID: 1, Subject: "orb strategy", Confidence: 0.85},
		{ID: 3, MemoryID: 2, Subject: "trading", Confidence: 0.92},
		{ID: 4, MemoryID: 2, Subject: "orb strategy", Confidence: 0.87},
		{ID: 5, MemoryID: 3, Subject: "trading", Confidence: 0.88},
		{ID: 6, MemoryID: 3, Subject: "alpaca", Confidence: 0.81},
	}

	result := BuildTopicClusters(facts)
	cluster := findClusterContaining(result.Clusters, "trading")
	if cluster == nil {
		t.Fatal("expected trading cluster")
	}

	if !strings.EqualFold(cluster.Name, "trading") {
		t.Fatalf("expected cluster name 'trading', got %q", cluster.Name)
	}
	if len(cluster.Aliases) == 0 {
		t.Fatalf("expected aliases for cluster, got none")
	}
}

func TestClusterMergeSmall(t *testing.T) {
	facts := []ClusterFact{
		// Large cluster backbone with strong edges.
		{ID: 1, MemoryID: 1, Subject: "trading", Confidence: 0.9},
		{ID: 2, MemoryID: 1, Subject: "orb", Confidence: 0.85},
		{ID: 3, MemoryID: 2, Subject: "trading", Confidence: 0.91},
		{ID: 4, MemoryID: 2, Subject: "orb", Confidence: 0.84},
		{ID: 5, MemoryID: 3, Subject: "trading", Confidence: 0.9},
		{ID: 6, MemoryID: 3, Subject: "options", Confidence: 0.82},
		{ID: 7, MemoryID: 4, Subject: "trading", Confidence: 0.91},
		{ID: 8, MemoryID: 4, Subject: "options", Confidence: 0.81},

		// Tiny component that should merge into trading by nearest edge weight.
		{ID: 9, MemoryID: 5, Subject: "scanner", Confidence: 0.76},
		{ID: 10, MemoryID: 5, Subject: "scanner bot", Confidence: 0.74},
		{ID: 11, MemoryID: 6, Subject: "scanner", Confidence: 0.77},
		{ID: 12, MemoryID: 6, Subject: "trading", Confidence: 0.88},
	}

	result := BuildTopicClusters(facts)
	cluster := findClusterContaining(result.Clusters, "trading")
	if cluster == nil {
		t.Fatal("expected trading cluster")
	}

	if !hasAllSubjects(*cluster, "scanner", "scanner bot") {
		t.Fatalf("expected tiny cluster subjects to merge into trading cluster; subjects=%v", cluster.Subjects)
	}
}

func findClusterContaining(clusters []TopicCluster, subject string) *TopicCluster {
	want := strings.ToLower(strings.TrimSpace(subject))
	for i := range clusters {
		for _, s := range clusters[i].Subjects {
			if strings.ToLower(strings.TrimSpace(s)) == want {
				return &clusters[i]
			}
		}
	}
	return nil
}

func hasAllSubjects(cluster TopicCluster, subjects ...string) bool {
	set := make(map[string]struct{}, len(cluster.Subjects))
	for _, s := range cluster.Subjects {
		set[strings.ToLower(strings.TrimSpace(s))] = struct{}{}
	}
	for _, s := range subjects {
		if _, ok := set[strings.ToLower(strings.TrimSpace(s))]; !ok {
			return false
		}
	}
	return true
}
