package ask

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/hurttlocker/cortex/internal/llm"
	"github.com/hurttlocker/cortex/internal/search"
)

var citationRefRE = regexp.MustCompile(`\[(\d+)\]`)
var citationGroupRE = regexp.MustCompile(`\[(\d+(?:\s*[,;]\s*\d+)*)\]`)
var danglingCitationGroupRE = regexp.MustCompile(`\[(\d+(?:\s*[,;]\s*\d+)*)\s*$`)

type Citation struct {
	Index         int     `json:"index"`
	Source        string  `json:"source"`
	Score         float64 `json:"score"`
	MemoryID      int64   `json:"memory_id"`
	Facts         []int64 `json:"fact_ids,omitempty"`
	SourceSection string  `json:"source_section,omitempty"`
}

type Result struct {
	Question     string          `json:"question"`
	Answer       string          `json:"answer"`
	RawAnswer    string          `json:"raw_answer,omitempty"`
	Citations    []Citation      `json:"citations"`
	Degraded     bool            `json:"degraded"`
	Reason       string          `json:"reason,omitempty"`
	Error        string          `json:"error,omitempty"`
	Results      []search.Result `json:"results,omitempty"`
	Model        string          `json:"model,omitempty"`
	Provider     string          `json:"provider,omitempty"`
	Budget       int             `json:"budget,omitempty"`
	PackedTokens int             `json:"packed_tokens,omitempty"`
}

type Options struct {
	Question        string
	Results         []search.Result
	MaxSentences    int
	MaxContextChars int
	PerResultChars  int
	Model           string
	Provider        string
	Budget          int
	PackedTokens    int
}

type Engine struct {
	llm   llm.Provider
	model string
}

func NewEngine(provider llm.Provider, model string) *Engine {
	return &Engine{llm: provider, model: model}
}

func (e *Engine) Ask(ctx context.Context, opts Options) (*Result, error) {
	question := strings.TrimSpace(opts.Question)
	if question == "" {
		return nil, fmt.Errorf("question is required")
	}
	if opts.MaxSentences <= 0 {
		opts.MaxSentences = 6
	}
	if opts.MaxSentences > 12 {
		opts.MaxSentences = 12
	}
	if opts.PerResultChars <= 0 {
		opts.PerResultChars = 1000
	}
	if opts.MaxContextChars <= 0 {
		opts.MaxContextChars = 5500
	}

	results := opts.Results
	if len(results) == 0 {
		return &Result{
			Question:     question,
			Answer:       "not enough evidence",
			Degraded:     true,
			Reason:       "no_results",
			Results:      results,
			Budget:       opts.Budget,
			PackedTokens: opts.PackedTokens,
		}, nil
	}

	if e.llm == nil {
		return fallbackResult(question, results, opts, "no_llm_configured", ""), nil
	}

	ctxText := buildGroupedEvidenceContext(results, opts.MaxContextChars, opts.PerResultChars)
	if ctxText == "" {
		return &Result{
			Question:     question,
			Answer:       "not enough evidence",
			Degraded:     true,
			Reason:       "empty_context_after_sanitize",
			Results:      results,
			Model:        e.model,
			Provider:     providerOfModel(e.model),
			Budget:       opts.Budget,
			PackedTokens: opts.PackedTokens,
		}, nil
	}

	systemPrompt := strings.TrimSpace(`You are Cortex Ask, a memory-grounded synthesis layer.

Answer only from the supplied evidence.

Rules:
- Do not use outside knowledge.
- If the evidence is insufficient, answer exactly: not enough evidence.
- If the evidence conflicts, state the conflict explicitly and cite both sides.
- Prefer active, unsuperseded, higher-confidence evidence.
- Answer in the shortest form possible.
- For dates and times, give the exact date or narrowest exact timeframe stated in the evidence.
- For names, titles, places, and numbers, give the exact value from the evidence.
- Keep the answer concise and under the requested sentence limit.
- Put citations immediately after each sentence or clause. Do not wait until the end of the answer to cite.
- Every factual claim must cite one or more source indices like [1] or [2][4].
- Do not cite indices that were not supplied.`)

	userPrompt := fmt.Sprintf(
		"Question:\n%s\n\nAvailable evidence:\n%s\n\nWrite the shortest direct answer possible with inline citations after each sentence or clause. If the evidence is insufficient, reply exactly: not enough evidence.",
		question,
		ctxText,
	)

	resp, err := e.llm.Complete(ctx, userPrompt, llm.CompletionOpts{
		System:      systemPrompt,
		Temperature: 0.1,
		MaxTokens:   700,
		Model:       e.model,
	})
	if err != nil {
		return fallbackResult(question, results, opts, "llm_error", err.Error()), nil
	}

	answerText := strings.TrimSpace(resp)
	if answerText == "" {
		return fallbackResult(question, results, opts, "empty_llm_response", ""), nil
	}
	if isNotEnoughEvidence(answerText) {
		return &Result{
			Question:     question,
			Answer:       "not enough evidence",
			Citations:    nil,
			Degraded:     false,
			Results:      results,
			Model:        e.model,
			Provider:     providerOfModel(e.model),
			Budget:       opts.Budget,
			PackedTokens: opts.PackedTokens,
		}, nil
	}

	cites, ok := extractCitations(answerText, results)
	if !ok || len(cites) == 0 {
		if repairedAnswer, repairedCites, repaired := repairCitations(answerText, results); repaired {
			return &Result{
				Question:     question,
				Answer:       clampSentences(repairedAnswer, opts.MaxSentences),
				Citations:    repairedCites,
				Degraded:     false,
				Model:        e.model,
				Provider:     providerOfModel(e.model),
				Budget:       opts.Budget,
				PackedTokens: opts.PackedTokens,
			}, nil
		}
		res := fallbackResult(question, results, opts, "citation_integrity_failed", "")
		res.RawAnswer = answerText
		return res, nil
	}

	return &Result{
		Question:     question,
		Answer:       clampSentences(answerText, opts.MaxSentences),
		Citations:    cites,
		Degraded:     false,
		Model:        e.model,
		Provider:     providerOfModel(e.model),
		Budget:       opts.Budget,
		PackedTokens: opts.PackedTokens,
	}, nil
}

func buildGroupedEvidenceContext(results []search.Result, maxContextChars int, perResultChars int) string {
	type evidenceGroup struct {
		label  string
		blocks []string
	}

	groupOrder := make([]string, 0, len(results))
	groupMap := make(map[string]*evidenceGroup, len(results))
	for i, r := range results {
		clean, _ := sanitizeRetrieved(r.Content)
		clean = truncate(clean, perResultChars)
		key := search.SceneLabelForResult(r)
		group, ok := groupMap[key]
		if !ok {
			group = &evidenceGroup{label: key}
			groupMap[key] = group
			groupOrder = append(groupOrder, key)
		}
		block := fmt.Sprintf(
			"[%d] source:%s section:%s score:%.2f fact_ids:%s\n%s",
			i+1,
			sourceLabel(r),
			strings.TrimSpace(r.SourceSection),
			r.Score,
			formatFactIDs(r.FactIDs),
			clean,
		)
		group.blocks = append(group.blocks, block)
	}

	remaining := maxContextChars
	lines := make([]string, 0, len(results)*3)
	for i, key := range groupOrder {
		group := groupMap[key]
		header := fmt.Sprintf("Evidence group %d — %s", i+1, group.label)
		groupLines := []string{header}
		for _, block := range group.blocks {
			candidate := strings.Join(append(groupLines, block), "\n\n")
			if len(candidate)+1 > remaining {
				break
			}
			groupLines = append(groupLines, block)
		}
		if len(groupLines) == 1 {
			continue
		}
		groupText := strings.Join(groupLines, "\n\n")
		if len(groupText)+2 > remaining {
			break
		}
		lines = append(lines, groupText)
		remaining -= len(groupText) + 2
	}

	return strings.Join(lines, "\n\n")
}

func fallbackResult(question string, results []search.Result, opts Options, reason, errorDetail string) *Result {
	cites := make([]Citation, 0, len(results))
	for i, r := range results {
		cites = append(cites, Citation{
			Index:         i + 1,
			Source:        sourceLabel(r),
			Score:         r.Score,
			MemoryID:      r.MemoryID,
			Facts:         append([]int64(nil), r.FactIDs...),
			SourceSection: r.SourceSection,
		})
	}
	return &Result{
		Question:     question,
		Answer:       "LLM unavailable or citation validation failed; returning retrieved evidence.",
		Citations:    cites,
		Degraded:     true,
		Reason:       reason,
		Error:        errorDetail,
		Results:      results,
		Budget:       opts.Budget,
		PackedTokens: opts.PackedTokens,
	}
}

func extractCitations(answer string, results []search.Result) ([]Citation, bool) {
	ordered, ok := extractCitationIndexes(answer, len(results))
	if !ok || len(ordered) == 0 {
		return nil, false
	}
	sort.Ints(ordered)
	out := make([]Citation, 0, len(ordered))
	for _, idx := range ordered {
		r := results[idx-1]
		out = append(out, Citation{
			Index:         idx,
			Source:        sourceLabel(r),
			Score:         r.Score,
			MemoryID:      r.MemoryID,
			Facts:         append([]int64(nil), r.FactIDs...),
			SourceSection: r.SourceSection,
		})
	}
	return out, true
}

func extractCitationIndexes(answer string, resultCount int) ([]int, bool) {
	seen := map[int]struct{}{}
	ordered := []int{}
	addGroup := func(group string) bool {
		for _, part := range strings.FieldsFunc(group, func(r rune) bool {
			return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
		}) {
			if part == "" {
				continue
			}
			idx := atoiSafe(part)
			if idx <= 0 || idx > resultCount {
				return false
			}
			if _, ok := seen[idx]; ok {
				continue
			}
			seen[idx] = struct{}{}
			ordered = append(ordered, idx)
		}
		return true
	}

	matches := citationGroupRE.FindAllStringSubmatch(answer, -1)
	for _, m := range matches {
		if len(m) < 2 || !addGroup(m[1]) {
			return nil, false
		}
	}

	if m := danglingCitationGroupRE.FindStringSubmatch(answer); len(m) >= 2 {
		if !addGroup(m[1]) {
			return nil, false
		}
	}

	if len(ordered) == 0 {
		return nil, false
	}
	return ordered, true
}

func repairCitations(answer string, results []search.Result) (string, []Citation, bool) {
	cleaned := strings.TrimSpace(danglingCitationGroupRE.ReplaceAllString(answer, ""))
	if cleaned == "" {
		return "", nil, false
	}

	answerTokens := contentTokens(cleaned)
	if len(answerTokens) == 0 {
		return "", nil, false
	}

	type candidate struct {
		index   int
		overlap int
		score   float64
	}
	candidates := make([]candidate, 0, len(results))
	for i, r := range results {
		overlap := tokenOverlap(answerTokens, contentTokens(r.Content+"\n"+r.SourceSection))
		if overlap == 0 {
			continue
		}
		candidates = append(candidates, candidate{
			index:   i + 1,
			overlap: overlap,
			score:   r.Score,
		})
	}
	if len(candidates) == 0 {
		return "", nil, false
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].overlap != candidates[j].overlap {
			return candidates[i].overlap > candidates[j].overlap
		}
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].index < candidates[j].index
	})

	minOverlap := 2
	if len(answerTokens) <= 4 {
		minOverlap = 1
	}
	best := candidates[0].overlap
	if best < minOverlap {
		return "", nil, false
	}

	indexes := make([]int, 0, 2)
	for _, c := range candidates {
		if c.overlap < best-1 {
			break
		}
		indexes = append(indexes, c.index)
		if len(indexes) == 2 {
			break
		}
	}
	if len(indexes) == 0 {
		return "", nil, false
	}

	var b strings.Builder
	b.WriteString(cleaned)
	if !strings.HasSuffix(cleaned, ".") && !strings.HasSuffix(cleaned, "!") && !strings.HasSuffix(cleaned, "?") {
		b.WriteString(".")
	}
	for _, idx := range indexes {
		fmt.Fprintf(&b, " [%d]", idx)
	}
	repaired := strings.TrimSpace(b.String())
	cites, ok := extractCitations(repaired, results)
	if !ok || len(cites) == 0 {
		return "", nil, false
	}
	return repaired, cites, true
}

func contentTokens(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) <= 1 {
			continue
		}
		switch field {
		case "the", "and", "for", "with", "that", "this", "from", "into", "your", "their", "have", "has", "had", "are", "was", "were", "his", "her", "she", "him", "they", "them", "what", "when", "where", "which", "then", "than", "just", "very", "much", "more", "about":
			continue
		}
		out = append(out, field)
	}
	return out
}

func tokenOverlap(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]struct{}, len(b))
	for _, token := range b {
		set[token] = struct{}{}
	}
	seen := map[string]struct{}{}
	overlap := 0
	for _, token := range a {
		if _, dup := seen[token]; dup {
			continue
		}
		seen[token] = struct{}{}
		if _, ok := set[token]; ok {
			overlap++
		}
	}
	return overlap
}

func sanitizeRetrieved(content string) (clean string, stripped string) {
	if strings.TrimSpace(content) == "" {
		return "", ""
	}
	bad := []string{
		"ignore previous",
		"ignore all previous",
		"system prompt",
		"developer message",
		"you are chatgpt",
		"assistant:",
		"system:",
		"tool:",
		"### instruction",
	}
	kept := []string{}
	removed := []string{}
	for _, line := range strings.Split(content, "\n") {
		l := strings.ToLower(strings.TrimSpace(line))
		isBad := false
		for _, b := range bad {
			if strings.Contains(l, b) {
				isBad = true
				break
			}
		}
		if isBad {
			removed = append(removed, line)
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), strings.TrimSpace(strings.Join(removed, " | "))
}

func sourceLabel(result search.Result) string {
	source := result.SourceFile
	if source == "" {
		source = "(unknown source)"
	}
	if result.SourceLine > 0 {
		source = fmt.Sprintf("%s:%d", source, result.SourceLine)
	}
	if result.SourceSection != "" {
		source = fmt.Sprintf("%s#%s", source, result.SourceSection)
	}
	return source
}

func formatFactIDs(ids []int64) string {
	if len(ids) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%d", id))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func clampSentences(s string, maxSentences int) string {
	parts := splitSentences(s)
	if len(parts) <= maxSentences {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(strings.Join(parts[:maxSentences], " "))
}

func isNotEnoughEvidence(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.Trim(s, " .!?:;\"'")
	return s == "not enough evidence"
}

func splitSentences(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := make([]string, 0, 8)
	start := 0
	for i, r := range s {
		switch r {
		case '.', '!', '?':
			part := strings.TrimSpace(s[start : i+1])
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		part := strings.TrimSpace(s[start:])
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func atoiSafe(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

func providerOfModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if idx := strings.IndexByte(model, '/'); idx > 0 {
		return model[:idx]
	}
	return ""
}
