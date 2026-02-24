package graph

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hurttlocker/cortex/internal/store"
)

const (
	defaultTimelineDays          = 30
	defaultTimelineMinConfidence = 0.3
	maxTimelineRelatedSubjects   = 24
)

// TransitionType describes how a subject's knowledge changed across time.
type TransitionType string

const (
	TransitionSuperseded   TransitionType = "superseded"
	TransitionRefined      TransitionType = "refined"
	TransitionDecayed      TransitionType = "decayed"
	TransitionExpanded     TransitionType = "expanded"
	TransitionContradicted TransitionType = "contradicted"
)

// TimelineRange is the inclusive date range requested by the client.
type TimelineRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// TimelineFact is a fact rendered on the timeline.
type TimelineFact struct {
	ID         int64   `json:"id"`
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source,omitempty"`
}

// TimelineBucket groups facts into a single day/week/month unit.
type TimelineBucket struct {
	Date            string         `json:"date"`
	Facts           []TimelineFact `json:"facts"`
	RelatedSubjects []string       `json:"related_subjects"`
	FactCount       int            `json:"fact_count"`
	AvgConfidence   float64        `json:"avg_confidence"`
}

// TimelineTransition links two time points where knowledge changed.
type TimelineTransition struct {
	FromDate    string         `json:"from_date"`
	ToDate      string         `json:"to_date"`
	Type        TransitionType `json:"type"`
	FromFact    int64          `json:"from_fact,omitempty"`
	ToFact      int64          `json:"to_fact,omitempty"`
	Description string         `json:"description"`
}

// TimelineTrendPoint is a confidence trend sample for a bucket.
type TimelineTrendPoint struct {
	Date string  `json:"date"`
	Avg  float64 `json:"avg"`
}

// TimelineResponse is the /api/timeline payload.
type TimelineResponse struct {
	Subject            string               `json:"subject"`
	Range              TimelineRange        `json:"range"`
	Bucket             string               `json:"bucket"`
	Buckets            []TimelineBucket     `json:"buckets"`
	Transitions        []TimelineTransition `json:"transitions"`
	SubjectFirstSeen   string               `json:"subject_first_seen,omitempty"`
	SubjectLastUpdated string               `json:"subject_last_updated,omitempty"`
	TotalFactsOverTime int                  `json:"total_facts_over_time"`
	ConfidenceTrend    []TimelineTrendPoint `json:"confidence_trend"`
}

type timelineQuery struct {
	Subject        string
	From           time.Time
	To             time.Time
	Bucket         string
	MinConfidence  float64
	IncludeRelated bool
}

type timelineFactRow struct {
	ID           int64
	Subject      string
	Predicate    string
	Object       string
	Confidence   float64
	Source       string
	CreatedAt    time.Time
	IsSuperseded bool
}

func handleTimelineAPI(w http.ResponseWriter, r *http.Request, st *store.SQLiteStore) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	opts, err := parseTimelineQuery(r)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}

	ctx := context.Background()
	db := st.GetDB()

	subjectExists, firstSeen, lastSeen, err := timelineSubjectWindow(ctx, db, opts.Subject)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !subjectExists {
		writeJSON(w, 404, map[string]string{"error": fmt.Sprintf("subject %q not found", opts.Subject)})
		return
	}

	response, err := buildTimelineResponse(ctx, db, opts)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	response.SubjectFirstSeen = firstSeen
	response.SubjectLastUpdated = lastSeen

	writeJSON(w, 200, response)
}

func parseTimelineQuery(r *http.Request) (timelineQuery, error) {
	now := time.Now().UTC()
	defaultFrom := truncateToDayUTC(now.AddDate(0, 0, -defaultTimelineDays))
	defaultTo := truncateToDayUTC(now)

	subject := strings.TrimSpace(r.URL.Query().Get("subject"))
	if subject == "" {
		return timelineQuery{}, fmt.Errorf("subject parameter required")
	}

	from, err := parseTimelineDate(r.URL.Query().Get("from"), defaultFrom)
	if err != nil {
		return timelineQuery{}, fmt.Errorf("invalid from date (expected YYYY-MM-DD)")
	}
	to, err := parseTimelineDate(r.URL.Query().Get("to"), defaultTo)
	if err != nil {
		return timelineQuery{}, fmt.Errorf("invalid to date (expected YYYY-MM-DD)")
	}
	if to.Before(from) {
		return timelineQuery{}, fmt.Errorf("to date must be on or after from date")
	}

	bucket := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("bucket")))
	if bucket == "" {
		bucket = "day"
	}
	switch bucket {
	case "day", "week", "month":
	default:
		return timelineQuery{}, fmt.Errorf("invalid bucket %q (valid: day, week, month)", bucket)
	}

	minConfidence := defaultTimelineMinConfidence
	if raw := strings.TrimSpace(r.URL.Query().Get("min_confidence")); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return timelineQuery{}, fmt.Errorf("invalid min_confidence")
		}
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		minConfidence = v
	}

	includeRelated, err := parseTimelineBool(r.URL.Query().Get("related"), true)
	if err != nil {
		return timelineQuery{}, fmt.Errorf("invalid related value")
	}

	return timelineQuery{
		Subject:        subject,
		From:           from,
		To:             to,
		Bucket:         bucket,
		MinConfidence:  minConfidence,
		IncludeRelated: includeRelated,
	}, nil
}

func parseTimelineDate(raw string, fallback time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, err
	}
	return truncateToDayUTC(t), nil
}

func parseTimelineBool(raw string, fallback bool) (bool, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return fallback, nil
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid bool")
	}
}

func truncateToDayUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func timelineSubjectWindow(ctx context.Context, db *sql.DB, subject string) (bool, string, string, error) {
	var count int
	var first string
	var last string
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*),
		        COALESCE(MIN(substr(created_at, 1, 10)), ''),
		        COALESCE(MAX(substr(created_at, 1, 10)), '')
		 FROM facts
		 WHERE LOWER(subject) = LOWER(?)`,
		subject,
	).Scan(&count, &first, &last)
	if err != nil {
		return false, "", "", err
	}
	return count > 0, first, last, nil
}

func buildTimelineResponse(ctx context.Context, db *sql.DB, opts timelineQuery) (TimelineResponse, error) {
	relatedSubjects := []string{}
	if opts.IncludeRelated {
		subs, err := loadTimelineRelatedSubjects(ctx, db, opts.Subject, opts.From, opts.To, opts.MinConfidence)
		if err != nil {
			return TimelineResponse{}, err
		}
		relatedSubjects = subs
	}

	subjects := make([]string, 0, 1+len(relatedSubjects))
	subjects = append(subjects, opts.Subject)
	subjects = append(subjects, relatedSubjects...)

	rows, err := loadTimelineFacts(ctx, db, subjects, opts.Subject, opts.From, opts.To, opts.MinConfidence)
	if err != nil {
		return TimelineResponse{}, err
	}

	buckets, trend, mainFactsCount := buildTimelineBuckets(rows, opts.Subject, opts.Bucket)
	transitions := detectTimelineTransitions(rows, buckets, opts.Subject, opts.Bucket, opts.MinConfidence, opts.IncludeRelated)

	return TimelineResponse{
		Subject: opts.Subject,
		Range: TimelineRange{
			From: opts.From.Format("2006-01-02"),
			To:   opts.To.Format("2006-01-02"),
		},
		Bucket:             opts.Bucket,
		Buckets:            buckets,
		Transitions:        transitions,
		TotalFactsOverTime: mainFactsCount,
		ConfidenceTrend:    trend,
	}, nil
}

func loadTimelineFacts(ctx context.Context, db *sql.DB, subjects []string, mainSubject string, from, to time.Time, minConfidence float64) ([]timelineFactRow, error) {
	if len(subjects) == 0 {
		return nil, nil
	}

	seen := make(map[string]string, len(subjects))
	normalized := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		subject = strings.TrimSpace(subject)
		if subject == "" {
			continue
		}
		key := strings.ToLower(subject)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = subject
		normalized = append(normalized, key)
	}
	if len(normalized) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(normalized)), ",")
	args := make([]interface{}, 0, len(normalized)+3)
	for _, subject := range normalized {
		args = append(args, subject)
	}
	args = append(args, from.Format("2006-01-02"), to.Format("2006-01-02"), mainSubject, minConfidence)

	query := fmt.Sprintf(
		`SELECT f.id,
		        f.subject,
		        f.predicate,
		        f.object,
		        f.confidence,
		        COALESCE(m.source_file, ''),
		        f.created_at,
		        f.superseded_by
		FROM facts f
		LEFT JOIN memories m ON m.id = f.memory_id
		WHERE LOWER(f.subject) IN (%s)
		  AND substr(f.created_at, 1, 10) >= ?
		  AND substr(f.created_at, 1, 10) <= ?
		  AND (LOWER(f.subject) = LOWER(?) OR f.confidence >= ? OR f.superseded_by IS NOT NULL)
		ORDER BY f.created_at ASC, f.id ASC`,
		placeholders,
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	facts := make([]timelineFactRow, 0)
	for rows.Next() {
		var item timelineFactRow
		var superseded sql.NullInt64
		if err := rows.Scan(
			&item.ID,
			&item.Subject,
			&item.Predicate,
			&item.Object,
			&item.Confidence,
			&item.Source,
			&item.CreatedAt,
			&superseded,
		); err != nil {
			return nil, err
		}
		item.CreatedAt = item.CreatedAt.UTC()
		item.IsSuperseded = superseded.Valid && superseded.Int64 > 0
		facts = append(facts, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return facts, nil
}

func loadTimelineRelatedSubjects(ctx context.Context, db *sql.DB, subject string, from, to time.Time, minConfidence float64) ([]string, error) {
	args := []interface{}{
		subject,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
		minConfidence,
		subject,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
		maxTimelineRelatedSubjects,
	}

	query := `WITH base AS (
		SELECT id
		FROM facts
		WHERE LOWER(subject) = LOWER(?)
		  AND substr(created_at, 1, 10) >= ?
		  AND substr(created_at, 1, 10) <= ?
		  AND (confidence >= ? OR superseded_by IS NOT NULL)
	), neighbors AS (
		SELECT DISTINCT
		  CASE
		    WHEN e.source_fact_id IN (SELECT id FROM base) THEN e.target_fact_id
		    ELSE e.source_fact_id
		  END AS fact_id
		FROM fact_edges_v1 e
		WHERE e.source_fact_id IN (SELECT id FROM base)
		   OR e.target_fact_id IN (SELECT id FROM base)
	)
	SELECT f.subject
	FROM neighbors n
	JOIN facts f ON f.id = n.fact_id
	WHERE LOWER(f.subject) != LOWER(?)
	  AND TRIM(COALESCE(f.subject, '')) != ''
	  AND substr(f.created_at, 1, 10) >= ?
	  AND substr(f.created_at, 1, 10) <= ?
	GROUP BY LOWER(f.subject)
	ORDER BY MAX(f.confidence) DESC, MAX(f.created_at) DESC
	LIMIT ?`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		if isMissingTableErr(err, "fact_edges_v1") {
			return loadTimelineRelatedSubjectsFallback(ctx, db, subject, from, to, minConfidence)
		}
		return nil, err
	}
	defer rows.Close()

	subjects := make([]string, 0)
	seen := make(map[string]bool)
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			return nil, err
		}
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if key == strings.ToLower(subject) || seen[key] {
			continue
		}
		seen[key] = true
		subjects = append(subjects, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return subjects, nil
}

func loadTimelineRelatedSubjectsFallback(ctx context.Context, db *sql.DB, subject string, from, to time.Time, minConfidence float64) ([]string, error) {
	query := `WITH base_memories AS (
		SELECT DISTINCT memory_id
		FROM facts
		WHERE LOWER(subject) = LOWER(?)
		  AND substr(created_at, 1, 10) >= ?
		  AND substr(created_at, 1, 10) <= ?
		  AND (confidence >= ? OR superseded_by IS NOT NULL)
	)
	SELECT f.subject
	FROM facts f
	WHERE f.memory_id IN (SELECT memory_id FROM base_memories)
	  AND LOWER(f.subject) != LOWER(?)
	  AND TRIM(COALESCE(f.subject, '')) != ''
	  AND substr(f.created_at, 1, 10) >= ?
	  AND substr(f.created_at, 1, 10) <= ?
	GROUP BY LOWER(f.subject)
	ORDER BY MAX(f.confidence) DESC, MAX(f.created_at) DESC
	LIMIT ?`

	rows, err := db.QueryContext(ctx, query,
		subject,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
		minConfidence,
		subject,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
		maxTimelineRelatedSubjects,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subjects []string
	seen := make(map[string]bool)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		k := strings.ToLower(s)
		if k == strings.ToLower(subject) || seen[k] {
			continue
		}
		seen[k] = true
		subjects = append(subjects, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return subjects, nil
}

type timelineBucketAgg struct {
	DateStart   time.Time
	Facts       []TimelineFact
	RelatedSet  map[string]bool
	SumConf     float64
	TrendSum    float64
	TrendCount  int
	MainFactIDs []int64
}

func buildTimelineBuckets(rows []timelineFactRow, mainSubject, bucket string) ([]TimelineBucket, []TimelineTrendPoint, int) {
	bucketMap := make(map[string]*timelineBucketAgg)
	mainFactsCount := 0

	for _, row := range rows {
		bucketStart := timelineBucketStart(row.CreatedAt, bucket)
		bucketDate := bucketStart.Format("2006-01-02")
		agg, ok := bucketMap[bucketDate]
		if !ok {
			agg = &timelineBucketAgg{
				DateStart:  bucketStart,
				Facts:      make([]TimelineFact, 0, 8),
				RelatedSet: make(map[string]bool),
			}
			bucketMap[bucketDate] = agg
		}

		agg.Facts = append(agg.Facts, TimelineFact{
			ID:         row.ID,
			Subject:    row.Subject,
			Predicate:  row.Predicate,
			Object:     row.Object,
			Confidence: row.Confidence,
			Source:     row.Source,
		})
		agg.SumConf += row.Confidence

		if strings.EqualFold(row.Subject, mainSubject) {
			mainFactsCount += 1
			agg.TrendSum += row.Confidence
			agg.TrendCount += 1
			agg.MainFactIDs = append(agg.MainFactIDs, row.ID)
		} else {
			agg.RelatedSet[row.Subject] = true
		}
	}

	ordered := make([]*timelineBucketAgg, 0, len(bucketMap))
	for _, agg := range bucketMap {
		ordered = append(ordered, agg)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].DateStart.Before(ordered[j].DateStart)
	})

	buckets := make([]TimelineBucket, 0, len(ordered))
	trend := make([]TimelineTrendPoint, 0, len(ordered))
	for _, agg := range ordered {
		sort.Slice(agg.Facts, func(i, j int) bool {
			if agg.Facts[i].Confidence == agg.Facts[j].Confidence {
				return agg.Facts[i].ID < agg.Facts[j].ID
			}
			return agg.Facts[i].Confidence > agg.Facts[j].Confidence
		})

		related := make([]string, 0, len(agg.RelatedSet))
		for subject := range agg.RelatedSet {
			related = append(related, subject)
		}
		sort.Strings(related)

		avg := 0.0
		if len(agg.Facts) > 0 {
			avg = agg.SumConf / float64(len(agg.Facts))
		}

		bucketDate := agg.DateStart.Format("2006-01-02")
		buckets = append(buckets, TimelineBucket{
			Date:            bucketDate,
			Facts:           agg.Facts,
			RelatedSubjects: related,
			FactCount:       len(agg.Facts),
			AvgConfidence:   avg,
		})

		if agg.TrendCount > 0 {
			trend = append(trend, TimelineTrendPoint{
				Date: bucketDate,
				Avg:  agg.TrendSum / float64(agg.TrendCount),
			})
		}
	}

	return buckets, trend, mainFactsCount
}

func timelineBucketStart(ts time.Time, bucket string) time.Time {
	ts = truncateToDayUTC(ts)
	switch bucket {
	case "week":
		weekday := int(ts.Weekday())
		offset := (weekday + 6) % 7 // Monday = 0
		return ts.AddDate(0, 0, -offset)
	case "month":
		return time.Date(ts.Year(), ts.Month(), 1, 0, 0, 0, 0, time.UTC)
	default:
		return ts
	}
}

func detectTimelineTransitions(rows []timelineFactRow, buckets []TimelineBucket, mainSubject, bucket string, minConfidence float64, includeRelated bool) []TimelineTransition {
	mainFacts := make([]timelineFactRow, 0)
	for _, row := range rows {
		if strings.EqualFold(row.Subject, mainSubject) {
			mainFacts = append(mainFacts, row)
		}
	}
	if len(mainFacts) == 0 {
		return []TimelineTransition{}
	}

	sort.Slice(mainFacts, func(i, j int) bool {
		if mainFacts[i].CreatedAt.Equal(mainFacts[j].CreatedAt) {
			return mainFacts[i].ID < mainFacts[j].ID
		}
		return mainFacts[i].CreatedAt.Before(mainFacts[j].CreatedAt)
	})

	transitions := make([]TimelineTransition, 0)
	seen := make(map[string]bool)

	add := func(tr TimelineTransition) {
		key := fmt.Sprintf("%s|%s|%s|%d|%d", tr.Type, tr.FromDate, tr.ToDate, tr.FromFact, tr.ToFact)
		if seen[key] {
			return
		}
		seen[key] = true
		transitions = append(transitions, tr)
	}

	lastByPredicate := make(map[string]timelineFactRow)
	for _, fact := range mainFacts {
		predKey := normalizeTimelinePredicate(fact.Predicate)
		if predKey == "" {
			continue
		}
		if prev, ok := lastByPredicate[predKey]; ok {
			if !strings.EqualFold(strings.TrimSpace(prev.Object), strings.TrimSpace(fact.Object)) &&
				fact.Confidence > prev.Confidence+0.05 {
				add(TimelineTransition{
					FromDate: timelineDateForFact(prev, bucket),
					ToDate:   timelineDateForFact(fact, bucket),
					Type:     TransitionSuperseded,
					FromFact: prev.ID,
					ToFact:   fact.ID,
					Description: fmt.Sprintf(
						"%s replaced by %s",
						shortObjectForTransition(prev.Object),
						shortObjectForTransition(fact.Object),
					),
				})
			}
		}
		lastByPredicate[predKey] = fact
	}

	historyByFact := make(map[string][]timelineFactRow)
	for _, fact := range mainFacts {
		key := fmt.Sprintf("%s|%s|%s",
			strings.ToLower(strings.TrimSpace(fact.Subject)),
			normalizeTimelinePredicate(fact.Predicate),
			strings.ToLower(strings.TrimSpace(fact.Object)),
		)
		historyByFact[key] = append(historyByFact[key], fact)
	}
	for _, history := range historyByFact {
		if len(history) < 2 {
			continue
		}
		sort.Slice(history, func(i, j int) bool {
			if history[i].CreatedAt.Equal(history[j].CreatedAt) {
				return history[i].ID < history[j].ID
			}
			return history[i].CreatedAt.Before(history[j].CreatedAt)
		})
		for i := 1; i < len(history); i++ {
			prev := history[i-1]
			curr := history[i]
			delta := curr.Confidence - prev.Confidence
			if delta > 0.05 {
				add(TimelineTransition{
					FromDate: timelineDateForFact(prev, bucket),
					ToDate:   timelineDateForFact(curr, bucket),
					Type:     TransitionRefined,
					FromFact: prev.ID,
					ToFact:   curr.ID,
					Description: fmt.Sprintf(
						"Confidence increased from %.2f to %.2f", prev.Confidence, curr.Confidence,
					),
				})
			}
			if delta < -0.05 || (prev.Confidence >= minConfidence && curr.Confidence < minConfidence) {
				add(TimelineTransition{
					FromDate: timelineDateForFact(prev, bucket),
					ToDate:   timelineDateForFact(curr, bucket),
					Type:     TransitionDecayed,
					FromFact: prev.ID,
					ToFact:   curr.ID,
					Description: fmt.Sprintf(
						"Confidence dropped from %.2f to %.2f", prev.Confidence, curr.Confidence,
					),
				})
			}
		}
	}

	activeByPredicate := make(map[string][]timelineFactRow)
	for _, fact := range mainFacts {
		if fact.IsSuperseded {
			continue
		}
		predKey := normalizeTimelinePredicate(fact.Predicate)
		if predKey == "" {
			continue
		}
		activeByPredicate[predKey] = append(activeByPredicate[predKey], fact)
	}
	for predicate, facts := range activeByPredicate {
		if len(facts) < 2 {
			continue
		}
		uniqueByObject := make(map[string]timelineFactRow)
		for _, fact := range facts {
			objKey := strings.ToLower(strings.TrimSpace(fact.Object))
			if objKey == "" {
				continue
			}
			if cur, ok := uniqueByObject[objKey]; ok {
				if fact.Confidence > cur.Confidence {
					uniqueByObject[objKey] = fact
				}
				continue
			}
			uniqueByObject[objKey] = fact
		}
		if len(uniqueByObject) < 2 {
			continue
		}

		candidates := make([]timelineFactRow, 0, len(uniqueByObject))
		for _, fact := range uniqueByObject {
			candidates = append(candidates, fact)
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].Confidence == candidates[j].Confidence {
				return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
			}
			return candidates[i].Confidence > candidates[j].Confidence
		})
		if len(candidates) >= 2 {
			a := candidates[0]
			b := candidates[1]
			if b.CreatedAt.Before(a.CreatedAt) {
				a, b = b, a
			}
			add(TimelineTransition{
				FromDate: timelineDateForFact(a, bucket),
				ToDate:   timelineDateForFact(b, bucket),
				Type:     TransitionContradicted,
				FromFact: a.ID,
				ToFact:   b.ID,
				Description: fmt.Sprintf(
					"Conflicting facts for %s: %s vs %s",
					predicate,
					shortObjectForTransition(a.Object),
					shortObjectForTransition(b.Object),
				),
			})
		}
	}

	if includeRelated {
		seenRelated := make(map[string]bool)
		prevDate := ""
		for _, b := range buckets {
			newSubjects := make([]string, 0)
			for _, subject := range b.RelatedSubjects {
				key := strings.ToLower(strings.TrimSpace(subject))
				if key == "" || seenRelated[key] {
					continue
				}
				seenRelated[key] = true
				newSubjects = append(newSubjects, subject)
			}
			sort.Strings(newSubjects)
			if prevDate != "" && len(newSubjects) > 0 {
				add(TimelineTransition{
					FromDate: prevDate,
					ToDate:   b.Date,
					Type:     TransitionExpanded,
					Description: fmt.Sprintf(
						"Neighborhood expanded: %s",
						strings.Join(newSubjects, ", "),
					),
				})
			}
			prevDate = b.Date
		}
	}

	sort.Slice(transitions, func(i, j int) bool {
		if transitions[i].FromDate == transitions[j].FromDate {
			if transitions[i].ToDate == transitions[j].ToDate {
				return transitions[i].Type < transitions[j].Type
			}
			return transitions[i].ToDate < transitions[j].ToDate
		}
		return transitions[i].FromDate < transitions[j].FromDate
	})

	return transitions
}

func normalizeTimelinePredicate(predicate string) string {
	predicate = strings.TrimSpace(strings.ToLower(predicate))
	if predicate == "" {
		return ""
	}
	return strings.Join(strings.Fields(predicate), " ")
}

func shortObjectForTransition(object string) string {
	object = strings.TrimSpace(object)
	if len(object) <= 44 {
		return object
	}
	return object[:41] + "..."
}

func timelineDateForFact(f timelineFactRow, bucket string) string {
	return timelineBucketStart(f.CreatedAt, bucket).Format("2006-01-02")
}
