package connect

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hurttlocker/cortex/internal/search"
	"github.com/hurttlocker/cortex/internal/store"
)

type integrationMockProvider struct {
	name         string
	displayName  string
	records      []Record
	respectSince bool
}

func (p *integrationMockProvider) Name() string {
	return p.name
}

func (p *integrationMockProvider) DisplayName() string {
	if p.displayName != "" {
		return p.displayName
	}
	return strings.Title(p.name)
}

func (p *integrationMockProvider) DefaultConfig() json.RawMessage {
	return json.RawMessage(`{}`)
}

func (p *integrationMockProvider) ValidateConfig(config json.RawMessage) error {
	return nil
}

func (p *integrationMockProvider) Fetch(ctx context.Context, cfg json.RawMessage, since *time.Time) ([]Record, error) {
	if !p.respectSince || since == nil {
		return append([]Record(nil), p.records...), nil
	}

	filtered := make([]Record, 0, len(p.records))
	for _, rec := range p.records {
		if rec.Timestamp.After(*since) {
			filtered = append(filtered, rec)
		}
	}
	return filtered, nil
}

func newIntegrationHarness(t *testing.T, providers ...Provider) (*SyncEngine, *ConnectorStore, *store.SQLiteStore, *search.Engine) {
	t.Helper()

	st, err := store.NewStore(store.StoreConfig{DBPath: ":memory:"})
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sqlSt, ok := st.(*store.SQLiteStore)
	if !ok {
		t.Fatal("expected SQLiteStore")
	}

	registry := NewRegistry()
	for _, p := range providers {
		registry.Register(p)
	}

	cs := NewConnectorStore(sqlSt.GetDB())
	engine := NewSyncEngine(registry, cs, sqlSt, false)
	searchEngine := search.NewEngine(sqlSt)
	return engine, cs, sqlSt, searchEngine
}

func TestConnectSyncExtractSearchE2E(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	provider := &integrationMockProvider{
		name:        "mock",
		displayName: "Mock",
		records: []Record{
			{
				ExternalID: "r1",
				Source:     "guild/acme/channel/general/msg/1",
				Content: `Alice is the CEO of Acme Corp since 2020.
Alice is in Acme Corp HQ.
Acme Corp is active.`,
				Timestamp: now.Add(-3 * time.Hour),
			},
			{
				ExternalID: "r2",
				Source:     "guild/acme/channel/general/msg/2",
				Content: `Acme Corp raised $10M in Series A.
Acme Corp is based in San Francisco.
Cortex is active.`,
				Timestamp: now.Add(-2 * time.Hour),
			},
			{
				ExternalID: "r3",
				Source:     "guild/acme/channel/general/msg/3",
				Content: `Bob joined Acme Corp as CTO in January 2024.
Bob is in Acme Corp HQ.
Cortex is running on port 7437.`,
				Timestamp: now.Add(-1 * time.Hour),
			},
		},
	}

	engine, cs, st, searchEngine := newIntegrationHarness(t, provider)

	if _, err := cs.Add(ctx, "mock", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("adding connector: %v", err)
	}
	conn, err := cs.Get(ctx, "mock")
	if err != nil {
		t.Fatalf("getting connector: %v", err)
	}

	result := engine.SyncOne(ctx, conn, SyncOptions{Extract: true})
	if result.Error != "" {
		t.Fatalf("sync error: %s", result.Error)
	}
	if result.RecordsImported != 3 {
		t.Fatalf("expected 3 imported records, got %d", result.RecordsImported)
	}
	if result.FactsExtracted == 0 {
		t.Fatal("expected extracted facts, got 0")
	}
	if result.EdgesInferred == 0 {
		t.Fatal("expected inferred edges, got 0")
	}

	facts, err := st.ListFacts(ctx, store.ListOpts{Limit: 300})
	if err != nil {
		t.Fatalf("listing facts: %v", err)
	}
	if len(facts) == 0 {
		t.Fatal("expected facts in store")
	}

	subjects := map[string]bool{}
	for _, f := range facts {
		sub := strings.ToLower(strings.TrimSpace(f.Subject))
		subjects[sub] = true
	}

	for _, expected := range []string{"alice", "acme corp", "bob"} {
		if !subjects[expected] {
			t.Fatalf("expected extracted subject %q in facts", expected)
		}
	}

	var inferredEdges int
	if err := st.QueryRowContext(ctx, `SELECT COUNT(*) FROM fact_edges_v1 WHERE source = 'inferred'`).Scan(&inferredEdges); err != nil {
		t.Fatalf("counting inferred edges: %v", err)
	}
	if inferredEdges == 0 {
		t.Fatal("expected inferred edges persisted to fact_edges_v1")
	}

	ceoResults, err := searchEngine.Search(ctx, "CEO", search.Options{Mode: search.ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("search CEO failed: %v", err)
	}
	if len(ceoResults) == 0 {
		t.Fatal("expected search results for CEO")
	}
	foundAlice := false
	for _, r := range ceoResults {
		if strings.Contains(strings.ToLower(r.Content), "alice") {
			foundAlice = true
			break
		}
	}
	if !foundAlice {
		t.Fatal("expected CEO search to include Alice record")
	}

	acmeSourceResults, err := searchEngine.Search(ctx, "Acme", search.Options{Mode: search.ModeKeyword, Limit: 20, Source: "mock"})
	if err != nil {
		t.Fatalf("source-filtered search failed: %v", err)
	}
	if len(acmeSourceResults) != 3 {
		t.Fatalf("expected 3 source-filtered results, got %d", len(acmeSourceResults))
	}

	manualContent := provider.records[0].Content
	_, err = st.AddMemory(ctx, &store.Memory{
		Content:       manualContent,
		SourceFile:    "manual/acme-notes.md",
		SourceSection: "Acme Manual",
	})
	if err != nil {
		t.Fatalf("adding manual memory: %v", err)
	}

	weighted, err := searchEngine.Search(ctx, "CEO Acme Corp", search.Options{Mode: search.ModeKeyword, Limit: 20})
	if err != nil {
		t.Fatalf("weighted search failed: %v", err)
	}

	manualScore := -1.0
	connectorScore := -1.0
	for _, r := range weighted {
		source := strings.ToLower(r.SourceFile)
		if strings.Contains(source, "manual/acme-notes.md") {
			manualScore = r.Score
		}
		if strings.HasPrefix(source, "mock:") {
			connectorScore = r.Score
		}
	}
	if manualScore < 0 || connectorScore < 0 {
		t.Fatalf("expected both manual and connector results, got manual=%f connector=%f", manualScore, connectorScore)
	}
	if manualScore <= connectorScore {
		t.Fatalf("expected manual source to score higher than connector source (manual=%f connector=%f)", manualScore, connectorScore)
	}
}

func TestConnectSyncIncrementalDedup(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	provider := &integrationMockProvider{
		name:        "dedup",
		displayName: "Dedup",
		records: []Record{
			{
				ExternalID: "d1",
				Source:     "channel/general/msg/1",
				Content:    "Cortex is running on port 7437.",
				Timestamp:  now.Add(-2 * time.Hour),
			},
			{
				ExternalID: "d2",
				Source:     "channel/general/msg/2",
				Content:    "Cortex is active.",
				Timestamp:  now.Add(-1 * time.Hour),
			},
		},
	}

	engine, cs, st, _ := newIntegrationHarness(t, provider)

	if _, err := cs.Add(ctx, "dedup", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("adding connector: %v", err)
	}
	conn, err := cs.Get(ctx, "dedup")
	if err != nil {
		t.Fatalf("getting connector: %v", err)
	}

	first := engine.SyncOne(ctx, conn, SyncOptions{Extract: true})
	if first.Error != "" {
		t.Fatalf("first sync error: %s", first.Error)
	}
	if first.RecordsImported != 2 {
		t.Fatalf("expected first sync imports=2, got %d", first.RecordsImported)
	}
	if first.FactsExtracted == 0 {
		t.Fatal("expected first sync to extract facts")
	}

	second := engine.SyncOne(ctx, conn, SyncOptions{Extract: true})
	if second.Error != "" {
		t.Fatalf("second sync error: %s", second.Error)
	}
	if second.RecordsImported != 0 {
		t.Fatalf("expected second sync imports=0, got %d", second.RecordsImported)
	}
	if second.FactsExtracted != 0 {
		t.Fatalf("expected second sync facts=0, got %d", second.FactsExtracted)
	}

	m, err := st.ListMemories(ctx, store.ListOpts{Limit: 20})
	if err != nil {
		t.Fatalf("listing memories: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("expected 2 memories after dedup, got %d", len(m))
	}
}

func TestConnectSyncMultiProviderSearch(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	providerA := &integrationMockProvider{
		name:        "alpha",
		displayName: "Alpha",
		records: []Record{{
			ExternalID: "a1",
			Source:     "channel/general/msg/1",
			Content:    "Alpha project update: rollout completed.",
			Timestamp:  now.Add(-2 * time.Hour),
		}},
	}
	providerB := &integrationMockProvider{
		name:        "beta",
		displayName: "Beta",
		records: []Record{{
			ExternalID: "b1",
			Source:     "channel/general/msg/1",
			Content:    "Beta project update: release candidate prepared.",
			Timestamp:  now.Add(-1 * time.Hour),
		}},
	}

	engine, cs, _, searchEngine := newIntegrationHarness(t, providerA, providerB)

	for _, name := range []string{"alpha", "beta"} {
		if _, err := cs.Add(ctx, name, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("adding %s connector: %v", name, err)
		}
	}

	results, err := engine.SyncAll(ctx)
	if err != nil {
		t.Fatalf("sync all failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 sync results, got %d", len(results))
	}

	all, err := searchEngine.Search(ctx, "project update", search.Options{Mode: search.ModeKeyword, Limit: 10})
	if err != nil {
		t.Fatalf("global search failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 unfiltered results, got %d", len(all))
	}

	alphaOnly, err := searchEngine.Search(ctx, "project update", search.Options{Mode: search.ModeKeyword, Limit: 10, Source: "alpha"})
	if err != nil {
		t.Fatalf("alpha search failed: %v", err)
	}
	if len(alphaOnly) != 1 {
		t.Fatalf("expected 1 alpha result, got %d", len(alphaOnly))
	}
	if !strings.HasPrefix(strings.ToLower(alphaOnly[0].SourceFile), "alpha:") {
		t.Fatalf("expected alpha source prefix, got %q", alphaOnly[0].SourceFile)
	}

	betaOnly, err := searchEngine.Search(ctx, "project update", search.Options{Mode: search.ModeKeyword, Limit: 10, Source: "beta"})
	if err != nil {
		t.Fatalf("beta search failed: %v", err)
	}
	if len(betaOnly) != 1 {
		t.Fatalf("expected 1 beta result, got %d", len(betaOnly))
	}
	if !strings.HasPrefix(strings.ToLower(betaOnly[0].SourceFile), "beta:") {
		t.Fatalf("expected beta source prefix, got %q", betaOnly[0].SourceFile)
	}
}

func TestConnectSyncWithEdgeInference(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	provider := &integrationMockProvider{
		name:        "infer",
		displayName: "Infer",
		records: []Record{{
			ExternalID: "i1",
			Source:     "channel/ops/msg/1",
			Content: `Cortex is running on port 7437.
Cortex is active.
Cortex is based in Austin.`,
			Timestamp: now.Add(-30 * time.Minute),
		}},
	}

	engine, cs, st, _ := newIntegrationHarness(t, provider)

	if _, err := cs.Add(ctx, "infer", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("adding connector: %v", err)
	}
	conn, err := cs.Get(ctx, "infer")
	if err != nil {
		t.Fatalf("getting connector: %v", err)
	}

	result := engine.SyncOne(ctx, conn, SyncOptions{Extract: true})
	if result.Error != "" {
		t.Fatalf("sync error: %s", result.Error)
	}
	if result.FactsExtracted == 0 {
		t.Fatal("expected extracted facts")
	}
	if result.EdgesInferred == 0 {
		t.Fatal("expected inferred edges")
	}

	edgeCount, err := st.CountEdges(ctx)
	if err != nil {
		t.Fatalf("counting edges: %v", err)
	}
	if edgeCount == 0 {
		t.Fatal("expected at least one stored edge")
	}

	var sourceFactID int64
	if err := st.QueryRowContext(ctx,
		`SELECT source_fact_id
		 FROM fact_edges_v1
		 WHERE source = 'inferred'
		 ORDER BY id DESC
		 LIMIT 1`,
	).Scan(&sourceFactID); err != nil {
		t.Fatalf("loading inferred edge source fact id: %v", err)
	}

	fact, err := st.GetFact(ctx, sourceFactID)
	if err != nil {
		t.Fatalf("loading source fact: %v", err)
	}
	if fact == nil {
		t.Fatal("expected source fact for inferred edge")
	}
	if !strings.EqualFold(fact.Subject, "cortex") {
		t.Fatalf("expected inferred edge to involve Cortex subject, got %q", fact.Subject)
	}
}
