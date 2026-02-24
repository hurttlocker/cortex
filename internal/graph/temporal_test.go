package graph

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/hurttlocker/cortex/internal/store"
)

func newTimelineServer(st *store.SQLiteStore) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/timeline", func(w http.ResponseWriter, r *http.Request) {
		handleTimelineAPI(w, r, st)
	})
	return httptest.NewServer(mux)
}

func addTimelineMemory(t *testing.T, st *store.SQLiteStore, source string) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := st.AddMemory(ctx, &store.Memory{
		Content:       "timeline test memory",
		SourceFile:    source,
		SourceLine:    1,
		SourceSection: "timeline",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	return id
}

func addTimelineFact(t *testing.T, st *store.SQLiteStore, memoryID int64, subject, predicate, object string, confidence float64, date string) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := st.AddFact(ctx, &store.Fact{
		MemoryID:   memoryID,
		Subject:    subject,
		Predicate:  predicate,
		Object:     object,
		FactType:   "kv",
		Confidence: confidence,
	})
	if err != nil {
		t.Fatalf("add fact: %v", err)
	}

	createdAt := date + " 12:00:00"
	if _, err := st.ExecContext(ctx, "UPDATE facts SET created_at = ?, confidence = ? WHERE id = ?", createdAt, confidence, id); err != nil {
		t.Fatalf("set created_at: %v", err)
	}
	return id
}

func decodeTimelineResponse(t *testing.T, resp *http.Response) TimelineResponse {
	t.Helper()
	defer resp.Body.Close()
	var out TimelineResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode timeline response: %v", err)
	}
	return out
}

func transitionTypes(transitions []TimelineTransition) map[TransitionType]bool {
	out := make(map[TransitionType]bool, len(transitions))
	for _, tr := range transitions {
		out[tr.Type] = true
	}
	return out
}

func TestTimelineBasicSubject(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	memID := addTimelineMemory(t, st, "timeline-basic.md")
	addTimelineFact(t, st, memID, "trading", "uses strategy", "EMA 7/28", 0.55, "2026-01-10")
	addTimelineFact(t, st, memID, "trading", "uses strategy", "ORB", 0.91, "2026-01-20")

	ts := newTimelineServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/timeline?subject=trading&from=2026-01-01&to=2026-01-31&bucket=day")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	out := decodeTimelineResponse(t, resp)
	if out.Subject != "trading" {
		t.Fatalf("expected subject=trading, got %q", out.Subject)
	}
	if out.Bucket != "day" {
		t.Fatalf("expected bucket=day, got %q", out.Bucket)
	}
	if len(out.Buckets) == 0 {
		t.Fatal("expected timeline buckets")
	}
	if out.TotalFactsOverTime != 2 {
		t.Fatalf("expected total_facts_over_time=2, got %d", out.TotalFactsOverTime)
	}
}

func TestTimelineDateRange(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	memID := addTimelineMemory(t, st, "timeline-range.md")
	addTimelineFact(t, st, memID, "project", "state", "started", 0.6, "2026-01-01")
	addTimelineFact(t, st, memID, "project", "state", "active", 0.8, "2026-02-01")

	ts := newTimelineServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/timeline?subject=project&from=2026-01-15&to=2026-02-10&bucket=day")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	out := decodeTimelineResponse(t, resp)
	if len(out.Buckets) != 1 {
		t.Fatalf("expected 1 bucket in date range, got %d", len(out.Buckets))
	}
	if out.Buckets[0].Date != "2026-02-01" {
		t.Fatalf("expected date 2026-02-01, got %q", out.Buckets[0].Date)
	}
}

func TestTimelineBucketDay(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	memID := addTimelineMemory(t, st, "timeline-day.md")
	addTimelineFact(t, st, memID, "infra", "status", "green", 0.8, "2026-01-11")
	addTimelineFact(t, st, memID, "infra", "status", "yellow", 0.7, "2026-01-12")

	ts := newTimelineServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/timeline?subject=infra&from=2026-01-10&to=2026-01-20&bucket=day")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	out := decodeTimelineResponse(t, resp)
	if len(out.Buckets) != 2 {
		t.Fatalf("expected 2 day buckets, got %d", len(out.Buckets))
	}
}

func TestTimelineBucketWeek(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	memID := addTimelineMemory(t, st, "timeline-week.md")
	addTimelineFact(t, st, memID, "infra", "status", "green", 0.8, "2026-01-12")  // Monday
	addTimelineFact(t, st, memID, "infra", "status", "yellow", 0.7, "2026-01-15") // Thursday same week

	ts := newTimelineServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/timeline?subject=infra&from=2026-01-10&to=2026-01-20&bucket=week")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	out := decodeTimelineResponse(t, resp)
	if len(out.Buckets) != 1 {
		t.Fatalf("expected 1 weekly bucket, got %d", len(out.Buckets))
	}
	if out.Buckets[0].Date != "2026-01-12" {
		t.Fatalf("expected week bucket date=2026-01-12, got %q", out.Buckets[0].Date)
	}
	if out.Buckets[0].FactCount != 2 {
		t.Fatalf("expected fact_count=2, got %d", out.Buckets[0].FactCount)
	}
}

func TestTimelineTransitionSuperseded(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	memID := addTimelineMemory(t, st, "timeline-superseded.md")
	addTimelineFact(t, st, memID, "trading", "uses strategy", "EMA", 0.45, "2026-01-15")
	addTimelineFact(t, st, memID, "trading", "uses strategy", "ORB", 0.95, "2026-02-09")

	ts := newTimelineServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/timeline?subject=trading&from=2026-01-01&to=2026-02-28&bucket=day")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	out := decodeTimelineResponse(t, resp)
	types := transitionTypes(out.Transitions)
	if !types[TransitionSuperseded] {
		t.Fatalf("expected superseded transition, got %v", out.Transitions)
	}
}

func TestTimelineTransitionRefined(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	memID := addTimelineMemory(t, st, "timeline-refined.md")
	addTimelineFact(t, st, memID, "trading", "risk model", "ATR", 0.51, "2026-01-10")
	addTimelineFact(t, st, memID, "trading", "risk model", "ATR", 0.86, "2026-01-25")

	ts := newTimelineServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/timeline?subject=trading&from=2026-01-01&to=2026-01-31&bucket=day")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	out := decodeTimelineResponse(t, resp)
	types := transitionTypes(out.Transitions)
	if !types[TransitionRefined] {
		t.Fatalf("expected refined transition, got %v", out.Transitions)
	}
}

func TestTimelineConfidenceTrend(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	memID := addTimelineMemory(t, st, "timeline-trend.md")
	addTimelineFact(t, st, memID, "research", "confidence", "low", 0.4, "2026-01-01")
	addTimelineFact(t, st, memID, "research", "confidence", "mid", 0.8, "2026-01-01")
	addTimelineFact(t, st, memID, "research", "confidence", "high", 0.9, "2026-01-02")

	ts := newTimelineServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/timeline?subject=research&from=2026-01-01&to=2026-01-03&bucket=day&min_confidence=0.0")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	out := decodeTimelineResponse(t, resp)
	if len(out.ConfidenceTrend) != 2 {
		t.Fatalf("expected 2 confidence trend points, got %d", len(out.ConfidenceTrend))
	}

	p0 := out.ConfidenceTrend[0]
	if p0.Date != "2026-01-01" {
		t.Fatalf("expected first trend date 2026-01-01, got %q", p0.Date)
	}
	if math.Abs(p0.Avg-0.6) > 0.001 {
		t.Fatalf("expected first trend avg ~0.6, got %.4f", p0.Avg)
	}
}

func TestTimelineRelatedSubjects(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ctx := context.Background()
	memID := addTimelineMemory(t, st, "timeline-related.md")
	mainFact := addTimelineFact(t, st, memID, "trading", "uses strategy", "ORB", 0.9, "2026-02-09")
	relatedFact := addTimelineFact(t, st, memID, "alpaca", "broker", "connected", 0.85, "2026-02-09")

	if err := st.AddEdge(ctx, &store.FactEdge{
		SourceFactID: mainFact,
		TargetFactID: relatedFact,
		EdgeType:     store.EdgeTypeRelatesTo,
		Confidence:   0.8,
		Source:       store.EdgeSourceExplicit,
	}); err != nil {
		t.Fatalf("add edge: %v", err)
	}

	ts := newTimelineServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/timeline?subject=trading&from=2026-02-01&to=2026-02-28&bucket=day&related=true")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	out := decodeTimelineResponse(t, resp)
	if len(out.Buckets) == 0 {
		t.Fatal("expected at least one bucket")
	}
	foundRelated := false
	for _, s := range out.Buckets[0].RelatedSubjects {
		if s == "alpaca" {
			foundRelated = true
			break
		}
	}
	if !foundRelated {
		t.Fatalf("expected related subject 'alpaca', got %v", out.Buckets[0].RelatedSubjects)
	}
}

func TestTimelineEmptyRange(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	memID := addTimelineMemory(t, st, "timeline-empty.md")
	addTimelineFact(t, st, memID, "design", "status", "draft", 0.7, "2026-01-10")

	ts := newTimelineServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/timeline?subject=design&from=2026-02-01&to=2026-02-10&bucket=day")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	out := decodeTimelineResponse(t, resp)
	if len(out.Buckets) != 0 {
		t.Fatalf("expected empty buckets, got %d", len(out.Buckets))
	}
	if len(out.ConfidenceTrend) != 0 {
		t.Fatalf("expected empty confidence trend, got %d", len(out.ConfidenceTrend))
	}
}

func TestTimelineUnknownSubject(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	ts := newTimelineServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/timeline?subject=missing&from=2026-01-01&to=2026-01-10")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 404 {
		t.Fatalf("expected 404 for unknown subject, got %d", resp.StatusCode)
	}
}

func TestTimelineTransitionDecayed(t *testing.T) {
	st := newTestStore(t)
	defer st.Close()

	memID := addTimelineMemory(t, st, "timeline-decayed.md")
	addTimelineFact(t, st, memID, "ops", "reliability", "stable", 0.82, "2026-01-03")
	addTimelineFact(t, st, memID, "ops", "reliability", "stable", 0.22, "2026-01-18")

	ts := newTimelineServer(st)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/timeline?subject=ops&from=2026-01-01&to=2026-01-31&min_confidence=" + strconv.FormatFloat(defaultTimelineMinConfidence, 'f', 1, 64))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	out := decodeTimelineResponse(t, resp)
	types := transitionTypes(out.Transitions)
	if !types[TransitionDecayed] {
		t.Fatalf("expected decayed transition, got %v", out.Transitions)
	}
}
