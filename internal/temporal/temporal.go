package temporal

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Norm struct {
	Kind        string  `json:"kind"`
	Literal     string  `json:"literal"`
	Value       string  `json:"value,omitempty"`
	Start       string  `json:"start,omitempty"`
	End         string  `json:"end,omitempty"`
	Anchor      string  `json:"anchor,omitempty"`
	Precision   string  `json:"precision,omitempty"`
	Resolution  string  `json:"resolution,omitempty"`
	CalendarRef string  `json:"calendar_ref,omitempty"`
	Confidence  float64 `json:"confidence,omitempty"`
}

type Query struct {
	Raw            string `json:"raw"`
	TemporalIntent bool   `json:"temporal_intent"`
	Kind           string `json:"kind,omitempty"`
	Value          string `json:"value,omitempty"`
	Start          string `json:"start,omitempty"`
	End            string `json:"end,omitempty"`
	Precision      string `json:"precision,omitempty"`
	Resolved       bool   `json:"resolved"`
}

var (
	sessionPrefixRE = regexp.MustCompile(`(?i)^\s*session\s+\d+\s*[-—:]\s*`)
	monthNames      = `(January|February|March|April|May|June|July|August|September|October|November|December)`
	dateTimeRE      = regexp.MustCompile(`(?i)\b\d{1,2}:\d{2}\s*(?:am|pm)\s+on\s+\d{1,2}\s+` + monthNames + `\s*,\s*\d{4}\b`)
	dayMonthYearRE  = regexp.MustCompile(`(?i)\b(\d{1,2})\s+` + monthNames + `\s*,?\s+(\d{4})\b`)
	monthDayYearRE  = regexp.MustCompile(`(?i)\b` + monthNames + `\s+(\d{1,2}),\s*(\d{4})\b`)
	dayMonthRE      = regexp.MustCompile(`(?i)\b(\d{1,2})\s+` + monthNames + `\b`)
	monthDayRE      = regexp.MustCompile(`(?i)\b` + monthNames + `\s+(\d{1,2})\b`)
	monthYearRE     = regexp.MustCompile(`(?i)\b` + monthNames + `\s+(\d{4})\b`)
	isoDateRE       = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)
	yearRE          = regexp.MustCompile(`\b((?:19|20)\d{2})\b`)
	weekBeforeRE    = regexp.MustCompile(`(?i)\b(?:the\s+)?week\s+before\s+(.+)$`)
	weekAfterRE     = regexp.MustCompile(`(?i)\b(?:the\s+)?week\s+after\s+(.+)$`)
	durationAgoRE   = regexp.MustCompile(`(?i)\b(\d+)\s+(day|week|month|year)s?\s+ago\b`)
	whenIntentRE    = regexp.MustCompile(`(?i)\b(when|date|dated|day|week|month|year|yesterday|today|tomorrow|last week|next week|last month|next month)\b`)
)

var anchorLayouts = []string{
	"3:04 pm on 2 January, 2006",
	"3:04 PM on 2 January, 2006",
	"3:04 pm on January 2, 2006",
	"3:04 PM on January 2, 2006",
	time.RFC3339,
	"2 January, 2006",
	"January 2, 2006",
	"2006-01-02",
}

func TimestampStartFromSection(section string) string {
	section = strings.TrimSpace(section)
	if section == "" {
		return ""
	}

	candidates := []string{section}
	cleaned := strings.TrimSpace(sessionPrefixRE.ReplaceAllString(section, ""))
	if cleaned != "" && cleaned != section {
		candidates = append(candidates, cleaned)
	}
	if match := dateTimeRE.FindString(cleaned); match != "" {
		candidates = append(candidates, match)
	}
	if match := dayMonthYearRE.FindString(cleaned); match != "" {
		candidates = append(candidates, match)
	}
	if match := monthDayYearRE.FindString(cleaned); match != "" {
		candidates = append(candidates, match)
	}
	if match := isoDateRE.FindString(cleaned); match != "" {
		candidates = append(candidates, match)
	}

	seen := map[string]struct{}{}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		if ts := parseAnchorTimestamp(c); ts != "" {
			return ts
		}
	}
	return ""
}

func ParseQuery(query string) *Query {
	raw := strings.TrimSpace(query)
	if raw == "" {
		return nil
	}
	q := &Query{Raw: raw}
	if whenIntentRE.MatchString(raw) {
		q.TemporalIntent = true
	}
	if norm := NormalizeLiteral(raw, ""); norm != nil {
		q.TemporalIntent = true
		q.Kind = norm.Kind
		q.Value = norm.Value
		q.Start = norm.Start
		q.End = norm.End
		q.Precision = norm.Precision
		q.Resolved = norm.Resolution != "unresolved"
	}
	if !q.TemporalIntent {
		return nil
	}
	return q
}

func NormalizeLiteral(literal string, anchor string) *Norm {
	literal = strings.TrimSpace(literal)
	if literal == "" {
		return nil
	}

	anchorTime, hasAnchor := parseAnchor(anchor)

	if norm := normalizeRelativeRange(literal, anchorTime, hasAnchor); norm != nil {
		return norm
	}
	if norm := normalizeRelativePoint(literal, anchorTime, hasAnchor); norm != nil {
		return norm
	}
	if norm := normalizeDurationAgo(literal, anchorTime, hasAnchor); norm != nil {
		return norm
	}
	if norm := normalizeAbsolute(literal, anchorTime, hasAnchor); norm != nil {
		return norm
	}

	if whenIntentRE.MatchString(literal) {
		return &Norm{
			Kind:       "date_range",
			Literal:    literal,
			Precision:  "unknown",
			Resolution: "unresolved",
			Anchor:     anchorDate(anchorTime, hasAnchor),
		}
	}
	return nil
}

func MatchesQuery(q *Query, n *Norm) bool {
	if q == nil || n == nil {
		return false
	}
	if q.Value != "" && n.Value != "" {
		return q.Value == n.Value
	}
	qs, qe := q.Start, q.End
	ns, ne := n.Start, n.End
	if qs != "" && qe == "" {
		qe = qs
	}
	if ns != "" && ne == "" {
		ne = ns
	}
	if qs == "" || qe == "" || ns == "" || ne == "" {
		return false
	}
	return !(qe < ns || ne < qs)
}

func Summary(n *Norm) string {
	if n == nil {
		return ""
	}
	switch {
	case n.Value != "":
		return n.Value
	case n.Start != "" && n.End != "" && n.Start != n.End:
		return n.Start + ".." + n.End
	case n.Start != "":
		return n.Start
	default:
		return ""
	}
}

func parseAnchorTimestamp(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, layout := range anchorLayouts {
		if parsed, err := time.ParseInLocation(layout, raw, time.UTC); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func parseAnchor(anchor string) (time.Time, bool) {
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, anchor, time.UTC); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func normalizeAbsolute(literal string, anchor time.Time, hasAnchor bool) *Norm {
	if match := isoDateRE.FindStringSubmatch(literal); len(match) == 2 {
		return &Norm{Kind: "date", Literal: literal, Value: match[1], Precision: "day", Resolution: "absolute", Confidence: 0.95}
	}
	if match := dayMonthYearRE.FindStringSubmatch(literal); len(match) == 4 {
		if t, err := time.ParseInLocation("2 January 2006", match[1]+" "+match[2]+" "+match[3], time.UTC); err == nil {
			return &Norm{Kind: "date", Literal: literal, Value: t.Format("2006-01-02"), Precision: "day", Resolution: "absolute", Confidence: 0.95}
		}
	}
	if match := monthDayYearRE.FindStringSubmatch(literal); len(match) == 4 {
		if t, err := time.ParseInLocation("January 2 2006", match[1]+" "+match[2]+" "+match[3], time.UTC); err == nil {
			return &Norm{Kind: "date", Literal: literal, Value: t.Format("2006-01-02"), Precision: "day", Resolution: "absolute", Confidence: 0.95}
		}
	}
	if match := monthYearRE.FindStringSubmatch(literal); len(match) == 3 {
		if t, err := time.ParseInLocation("January 2006", match[1]+" "+match[2], time.UTC); err == nil {
			start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
			end := start.AddDate(0, 1, -1)
			return &Norm{Kind: "date_range", Literal: literal, Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"), Precision: "month", Resolution: "absolute", Confidence: 0.9}
		}
	}
	if match := yearRE.FindStringSubmatch(literal); len(match) == 2 && !strings.Contains(strings.ToLower(literal), "ago") {
		year, _ := strconv.Atoi(match[1])
		start := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
		return &Norm{Kind: "date_range", Literal: literal, Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"), Precision: "year", Resolution: "absolute", Confidence: 0.85}
	}
	if hasAnchor {
		if match := dayMonthRE.FindStringSubmatch(literal); len(match) == 3 {
			if t, err := time.ParseInLocation("2 January 2006", match[1]+" "+match[2]+" "+strconv.Itoa(anchor.Year()), time.UTC); err == nil {
				return &Norm{Kind: "date", Literal: literal, Value: t.Format("2006-01-02"), Precision: "day", Resolution: "resolved_from_anchor", Anchor: anchor.Format("2006-01-02"), CalendarRef: "session_start", Confidence: 0.8}
			}
		}
		if match := monthDayRE.FindStringSubmatch(literal); len(match) == 3 {
			if t, err := time.ParseInLocation("January 2 2006", match[1]+" "+match[2]+" "+strconv.Itoa(anchor.Year()), time.UTC); err == nil {
				return &Norm{Kind: "date", Literal: literal, Value: t.Format("2006-01-02"), Precision: "day", Resolution: "resolved_from_anchor", Anchor: anchor.Format("2006-01-02"), CalendarRef: "session_start", Confidence: 0.8}
			}
		}
	}
	return nil
}

func normalizeRelativeRange(literal string, anchor time.Time, hasAnchor bool) *Norm {
	raw := strings.TrimSpace(literal)
	lower := strings.ToLower(raw)
	if match := weekBeforeRE.FindStringSubmatch(raw); len(match) == 2 {
		if base := NormalizeLiteral(match[1], anchorDate(anchor, hasAnchor)); base != nil {
			baseDate := valueToTime(base)
			if !baseDate.IsZero() {
				start := baseDate.AddDate(0, 0, -7)
				end := baseDate.AddDate(0, 0, -1)
				return &Norm{Kind: "date_range", Literal: literal, Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"), Anchor: anchorDate(anchor, hasAnchor), Precision: "day", Resolution: "resolved_from_anchor", CalendarRef: "session_start", Confidence: 0.9}
			}
		}
	}
	if match := weekAfterRE.FindStringSubmatch(raw); len(match) == 2 {
		if base := NormalizeLiteral(match[1], anchorDate(anchor, hasAnchor)); base != nil {
			baseDate := valueToTime(base)
			if !baseDate.IsZero() {
				start := baseDate.AddDate(0, 0, 1)
				end := baseDate.AddDate(0, 0, 7)
				return &Norm{Kind: "date_range", Literal: literal, Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"), Anchor: anchorDate(anchor, hasAnchor), Precision: "day", Resolution: "resolved_from_anchor", CalendarRef: "session_start", Confidence: 0.9}
			}
		}
	}
	if !hasAnchor {
		return nil
	}
	switch lower {
	case "last week":
		return rangeFromAnchor(literal, anchor, -7, -1)
	case "next week":
		return rangeFromAnchor(literal, anchor, 1, 7)
	case "last month":
		start := time.Date(anchor.Year(), anchor.Month()-1, 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, -1)
		return &Norm{Kind: "date_range", Literal: literal, Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"), Anchor: anchor.Format("2006-01-02"), Precision: "month", Resolution: "resolved_from_anchor", CalendarRef: "session_start", Confidence: 0.85}
	case "next month":
		start := time.Date(anchor.Year(), anchor.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, -1)
		return &Norm{Kind: "date_range", Literal: literal, Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"), Anchor: anchor.Format("2006-01-02"), Precision: "month", Resolution: "resolved_from_anchor", CalendarRef: "session_start", Confidence: 0.85}
	}
	return nil
}

func normalizeRelativePoint(literal string, anchor time.Time, hasAnchor bool) *Norm {
	if !hasAnchor {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(literal)) {
	case "today":
		return pointFromAnchor(literal, anchor, 0)
	case "yesterday":
		return pointFromAnchor(literal, anchor, -1)
	case "tomorrow":
		return pointFromAnchor(literal, anchor, 1)
	}
	return nil
}

func normalizeDurationAgo(literal string, anchor time.Time, hasAnchor bool) *Norm {
	match := durationAgoRE.FindStringSubmatch(strings.ToLower(strings.TrimSpace(literal)))
	if len(match) != 3 || !hasAnchor {
		return nil
	}
	amount, _ := strconv.Atoi(match[1])
	unit := match[2]
	switch unit {
	case "day":
		target := anchor.AddDate(0, 0, -amount)
		return &Norm{Kind: "date", Literal: literal, Value: target.Format("2006-01-02"), Anchor: anchor.Format("2006-01-02"), Precision: "day", Resolution: "resolved_from_anchor", CalendarRef: "session_start", Confidence: 0.8}
	case "week":
		start := anchor.AddDate(0, 0, -7*amount)
		end := start.AddDate(0, 0, 6)
		return &Norm{Kind: "date_range", Literal: literal, Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"), Anchor: anchor.Format("2006-01-02"), Precision: "week", Resolution: "resolved_from_anchor", CalendarRef: "session_start", Confidence: 0.8}
	case "month":
		target := anchor.AddDate(0, -amount, 0)
		start := time.Date(target.Year(), target.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, -1)
		return &Norm{Kind: "date_range", Literal: literal, Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"), Anchor: anchor.Format("2006-01-02"), Precision: "month", Resolution: "resolved_from_anchor", CalendarRef: "session_start", Confidence: 0.78}
	case "year":
		targetYear := anchor.Year() - amount
		start := time.Date(targetYear, time.January, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(targetYear, time.December, 31, 0, 0, 0, 0, time.UTC)
		return &Norm{Kind: "date_range", Literal: literal, Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"), Anchor: anchor.Format("2006-01-02"), Precision: "year", Resolution: "resolved_from_anchor", CalendarRef: "session_start", Confidence: 0.75}
	}
	return nil
}

func pointFromAnchor(literal string, anchor time.Time, days int) *Norm {
	target := anchor.AddDate(0, 0, days)
	return &Norm{Kind: "date", Literal: literal, Value: target.Format("2006-01-02"), Anchor: anchor.Format("2006-01-02"), Precision: "day", Resolution: "resolved_from_anchor", CalendarRef: "session_start", Confidence: 0.9}
}

func rangeFromAnchor(literal string, anchor time.Time, startDays int, endDays int) *Norm {
	start := anchor.AddDate(0, 0, startDays)
	end := anchor.AddDate(0, 0, endDays)
	return &Norm{Kind: "date_range", Literal: literal, Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"), Anchor: anchor.Format("2006-01-02"), Precision: "day", Resolution: "resolved_from_anchor", CalendarRef: "session_start", Confidence: 0.9}
}

func valueToTime(n *Norm) time.Time {
	if n == nil {
		return time.Time{}
	}
	if n.Value != "" {
		if t, err := time.ParseInLocation("2006-01-02", n.Value, time.UTC); err == nil {
			return t
		}
	}
	if n.Start != "" {
		if t, err := time.ParseInLocation("2006-01-02", n.Start, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}

func anchorDate(anchor time.Time, ok bool) string {
	if !ok {
		return ""
	}
	return anchor.Format("2006-01-02")
}
