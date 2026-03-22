package ask

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hurttlocker/cortex/internal/llm"
	"github.com/hurttlocker/cortex/internal/search"
)

var citationRefRE = regexp.MustCompile(`\[(\d+)\]`)

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
	Citations    []Citation      `json:"citations"`
	Degraded     bool            `json:"degraded"`
	Reason       string          `json:"reason,omitempty"`
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
		return fallbackResult(question, results, opts, "no_llm_configured"), nil
	}

	ctxLines := make([]string, 0, len(results)*4)
	remaining := opts.MaxContextChars
	for i, r := range results {
		clean, stripped := sanitizeRetrieved(r.Content)
		if stripped != "" {
			// ignore stripped content silently; the retrieval layer already surfaced clean evidence
		}
		clean = truncate(clean, opts.PerResultChars)
		block := fmt.Sprintf(
			"[%d] source:%s section:%s score:%.2f fact_ids:%s\n%s",
			i+1,
			sourceLabel(r),
			strings.TrimSpace(r.SourceSection),
			r.Score,
			formatFactIDs(r.FactIDs),
			clean,
		)
		if len(block)+1 > remaining {
			break
		}
		ctxLines = append(ctxLines, block)
		remaining -= len(block) + 1
	}
	if len(ctxLines) == 0 {
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
- Keep the answer concise and under the requested sentence limit.
- Every factual claim must cite one or more source indices like [1] or [2][4].
- Do not cite indices that were not supplied.`)

	userPrompt := fmt.Sprintf(
		"Question:\n%s\n\nAvailable evidence:\n%s\n\nWrite a direct answer with citations. If the evidence is insufficient, reply exactly: not enough evidence.",
		question,
		strings.Join(ctxLines, "\n\n"),
	)

	resp, err := e.llm.Complete(ctx, userPrompt, llm.CompletionOpts{
		System:      systemPrompt,
		Temperature: 0.1,
		MaxTokens:   700,
		Model:       e.model,
	})
	if err != nil {
		return fallbackResult(question, results, opts, "llm_error"), nil
	}

	answerText := strings.TrimSpace(resp)
	if answerText == "" {
		return fallbackResult(question, results, opts, "empty_llm_response"), nil
	}
	if strings.EqualFold(answerText, "not enough evidence") {
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
		return fallbackResult(question, results, opts, "citation_integrity_failed"), nil
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

func fallbackResult(question string, results []search.Result, opts Options, reason string) *Result {
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
		Results:      results,
		Budget:       opts.Budget,
		PackedTokens: opts.PackedTokens,
	}
}

func extractCitations(answer string, results []search.Result) ([]Citation, bool) {
	matches := citationRefRE.FindAllStringSubmatch(answer, -1)
	if len(matches) == 0 {
		return nil, false
	}
	seen := map[int]struct{}{}
	ordered := []int{}
	for _, m := range matches {
		idx := atoiSafe(m[1])
		if idx <= 0 || idx > len(results) {
			return nil, false
		}
		if _, ok := seen[idx]; !ok {
			seen[idx] = struct{}{}
			ordered = append(ordered, idx)
		}
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
