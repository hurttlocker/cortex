package ingest

import (
	_ "embed"
	"encoding/json"
	"math"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed import_keepdrop_model.json
var importKeepDropModelJSON []byte

type importKeepDropModel struct {
	Kind         string    `json:"kind"`
	Lowercase    bool      `json:"lowercase"`
	NgramRange   []int     `json:"ngram_range"`
	TokenPattern string    `json:"token_pattern"`
	Features     []string  `json:"features"`
	IDF          []float64 `json:"idf"`
	Coef         []float64 `json:"coef"`
	Intercept    float64   `json:"intercept"`
	Threshold    float64   `json:"threshold"`
}

type ImportKeepDropGate struct {
	model     importKeepDropModel
	featureIx map[string]int
	tokenRE   *regexp.Regexp
}

func NewImportKeepDropGate() (*ImportKeepDropGate, error) {
	var model importKeepDropModel
	if err := json.Unmarshal(importKeepDropModelJSON, &model); err != nil {
		return nil, err
	}
	index := make(map[string]int, len(model.Features))
	for i, feat := range model.Features {
		index[feat] = i
	}
	re := regexp.MustCompile(`[[:alnum:]_]{2,}`)
	return &ImportKeepDropGate{
		model:     model,
		featureIx: index,
		tokenRE:   re,
	}, nil
}

func (g *ImportKeepDropGate) Score(text string) float64 {
	if g == nil {
		return 1.0
	}
	features := g.vectorize(text)
	if len(features) == 0 {
		return 0
	}
	logit := g.model.Intercept
	for idx, value := range features {
		if idx < 0 || idx >= len(g.model.Coef) {
			continue
		}
		logit += value * g.model.Coef[idx]
	}
	return 1.0 / (1.0 + math.Exp(-logit))
}

func (g *ImportKeepDropGate) Keep(text string) bool {
	if g == nil {
		return true
	}
	return g.Score(text) >= g.model.Threshold
}

func (g *ImportKeepDropGate) ScoreRaw(raw RawMemory, memoryClass string) float64 {
	return g.Score(augmentImportQualityText(raw, memoryClass))
}

func (g *ImportKeepDropGate) KeepRaw(raw RawMemory, memoryClass string) bool {
	return g.ScoreRaw(raw, memoryClass) >= g.model.Threshold
}

func (g *ImportKeepDropGate) vectorize(text string) map[int]float64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if g.model.Lowercase {
		text = strings.ToLower(text)
	}
	tokens := g.tokenRE.FindAllString(text, -1)
	if len(tokens) == 0 {
		return nil
	}
	counts := make(map[int]float64)
	for _, token := range tokens {
		if idx, ok := g.featureIx[token]; ok {
			counts[idx]++
		}
	}
	for i := 0; i < len(tokens)-1; i++ {
		bigram := tokens[i] + " " + tokens[i+1]
		if idx, ok := g.featureIx[bigram]; ok {
			counts[idx]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	norm := 0.0
	for idx, tf := range counts {
		if idx >= len(g.model.IDF) {
			continue
		}
		value := tf * g.model.IDF[idx]
		counts[idx] = value
		norm += value * value
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for idx, value := range counts {
			counts[idx] = value / norm
		}
	}
	return counts
}

func augmentImportQualityText(raw RawMemory, memoryClass string) string {
	text := strings.TrimSpace(raw.Content)
	if text == "" {
		return text
	}
	tokens := make([]string, 0, 8)
	sourceFile := strings.ToLower(strings.TrimSpace(raw.SourceFile))
	sourceSection := strings.TrimSpace(raw.SourceSection)
	contentLen := len(text)
	lineCount := strings.Count(text, "\n") + 1

	if sourceSection != "" {
		tokens = append(tokens, "meta_has_source_section")
	}
	if isPositiveImportPath(sourceFile) {
		tokens = append(tokens, "meta_path_positive")
	}
	if isNegativeImportPath(sourceFile) {
		tokens = append(tokens, "meta_path_negative")
	}
	if mc := sanitizeImportToken(memoryClass); mc != "" {
		tokens = append(tokens, "meta_memory_class_"+mc)
	}
	switch {
	case contentLen < 20:
		tokens = append(tokens, "meta_len_tiny")
	case contentLen < 120:
		tokens = append(tokens, "meta_len_short")
	case contentLen <= 4000:
		tokens = append(tokens, "meta_len_medium")
	default:
		tokens = append(tokens, "meta_len_long")
	}
	if lineCount >= 3 {
		tokens = append(tokens, "meta_multiline")
	}
	lower := strings.ToLower(text)
	if importProtocolNoiseRE.MatchString(lower) {
		tokens = append(tokens, "meta_protocol_noise")
	}
	if contentLen < 140 && importLowSignalAckRE.MatchString(lower) {
		tokens = append(tokens, "meta_low_signal_ack")
	}
	if importCLICommandRE.MatchString(lower) {
		tokens = append(tokens, "meta_cli_command")
	}
	if importOpsKeywordRE.MatchString(lower) {
		tokens = append(tokens, "meta_ops_keyword")
	}
	if importEndpointOrConfigRE.MatchString(text) {
		tokens = append(tokens, "meta_endpoint_or_config")
	}
	if len(tokens) == 0 {
		return text
	}
	return strings.Join(tokens, " ") + "\n" + text
}

var (
	importPositivePathRE     = regexp.MustCompile(`(?i)(readme|memory|decision|rule|prd|docs/|design|spec|architecture|roadmap)`)
	importNegativePathRE     = regexp.MustCompile(`(?i)(/logs?/|/tmp/|/cache/|/node_modules/|/target/|\.log$|\.tmp$|\.out$)`)
	importLowSignalAckRE     = regexp.MustCompile(`(?i)\b(?:got it|sounds good|thank you|thanks!|okay|ok|cool|awesome|roger|copy that|noted)\b`)
	importCLICommandRE       = regexp.MustCompile(`(?i)\b(?:git|go|cortex|openclaw|npm|python3|pip|ollama|gh)\b`)
	importOpsKeywordRE       = regexp.MustCompile(`(?i)\b(?:gateway|restart|status|import|search|test|build|push|pull|deploy|alert|maintenance|db_size_high)\b`)
	importEndpointOrConfigRE = regexp.MustCompile(`(?i)(?:/[a-z0-9._~!$&'()*+,;=:@%/-]+|[A-Z_]{3,}=)`)
	importProtocolNoiseRE    = regexp.MustCompile(
		`(?i)\b(?:heartbeat|status: ok|health check|ping|pong|keepalive|session started|session ended|connected|disconnected|retrying|ws_token|bearer token|http 200|http 201|trace_id|request_id)\b`,
	)
)

func isPositiveImportPath(sourceFile string) bool {
	return importPositivePathRE.MatchString(sourceFile)
}

func isNegativeImportPath(sourceFile string) bool {
	return importNegativePathRE.MatchString(sourceFile)
}

func sanitizeImportToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	base := filepath.Base(value)
	base = strings.ReplaceAll(base, ".", "_")
	base = strings.ReplaceAll(base, "-", "_")
	base = strings.ReplaceAll(base, "/", "_")
	base = strings.Trim(base, "_")
	if base == "" {
		return ""
	}
	return base
}
