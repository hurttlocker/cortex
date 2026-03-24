package extract

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

//go:embed fact_type_model.json
var factTypeModelJSON []byte

const DefaultLocalClassifyModel = "embedded/fact-type-logreg-v1"

type localFactTypeModel struct {
	Kind         string      `json:"kind"`
	Lowercase    bool        `json:"lowercase"`
	NgramRange   []int       `json:"ngram_range"`
	TokenPattern string      `json:"token_pattern"`
	Features     []string    `json:"features"`
	IDF          []float64   `json:"idf"`
	Classes      []string    `json:"classes"`
	Coef         [][]float64 `json:"coef"`
	Intercept    []float64   `json:"intercept"`
	Threshold    float64     `json:"threshold"`
}

type LocalFactTypeClassifier struct {
	model     localFactTypeModel
	featureIx map[string]int
	tokenRE   *regexp.Regexp
}

var (
	localDateRE         = regexp.MustCompile(`(?i)\b(?:19|20)\d{2}[-/](?:0?[1-9]|1[0-2])[-/](?:0?[1-9]|[12]\d|3[01])\b`)
	localTimeRE         = regexp.MustCompile(`(?i)\b(?:[01]?\d|2[0-3]):[0-5]\d(?:\s?[ap]m)?\b`)
	localPathRE         = regexp.MustCompile(`(?i)(?:/[\w./-]+|[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+)`)
	localCommitRE       = regexp.MustCompile(`\b[a-f0-9]{7,40}\b`)
	localEnvRE          = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{2,}\b`)
	localNumericRE      = regexp.MustCompile(`^\s*[-+$]?[0-9][0-9,.:/%+\- ]*\s*$`)
	localEventLexRE     = regexp.MustCompile(`(?i)\b(?:added|built|shipped|fixed|removed|launched|crashed|merged|completed|updated|switched|validated|result|proof|closed issues)\b`)
	localRuleLexRE      = regexp.MustCompile(`(?i)\b(?:must|always|should|need to|run |keep |before |constraint|exit|threshold|goal|policy|rule|step)\b`)
	localDecisionLexRE  = regexp.MustCompile(`(?i)\b(?:decision|decided|choose|chose|approved|confirmed|parked_because|next-step)\b`)
	localConfigLexRE    = regexp.MustCompile(`(?i)\b(?:config|env|port|flag|mode|version|theme|icon|layout|output|setting|parameter|api key|style|smart mode)\b`)
	localRelationshipRE = regexp.MustCompile(`(?i)\b(?:manager|works on|co-founder|agent on|reports to|uses|manages|partner|owner)\b`)
	localIdentityRE     = regexp.MustCompile(`(?i)\b(?:email|phone|dob|birthday|name|credential|account|api key restored|ssh config)\b`)
	localPreferenceRE   = regexp.MustCompile(`(?i)\b(?:prefers|likes|dislikes|favorite|framing|wants)\b`)
	localLocationRE     = regexp.MustCompile(`(?i)\b(?:path|repo|branch|file|root|folder|directory|source_file|checkout on|main branch head)\b`)
	localTemporalLexRE  = regexp.MustCompile(`(?i)\b(?:today|yesterday|tomorrow|am|pm|et|deadline|expires|on feb|on mar|on jan|monday|tuesday|wednesday|thursday|friday)\b`)
	localStateLexRE     = regexp.MustCompile(`(?i)\b(?:status|running|blocked|idle|online|offline|pending|not filed yet|error|healthy|live)\b`)
)

func NewLocalFactTypeClassifier() (*LocalFactTypeClassifier, error) {
	var model localFactTypeModel
	if err := json.Unmarshal(factTypeModelJSON, &model); err != nil {
		return nil, err
	}
	index := make(map[string]int, len(model.Features))
	for i, feat := range model.Features {
		index[feat] = i
	}
	return &LocalFactTypeClassifier{
		model:     model,
		featureIx: index,
		tokenRE:   regexp.MustCompile(`[[:alnum:]_]{2,}`),
	}, nil
}

func (c *LocalFactTypeClassifier) Threshold() float64 {
	if c == nil || c.model.Threshold <= 0 {
		return 0.45
	}
	return c.model.Threshold
}

func (c *LocalFactTypeClassifier) Name() string {
	if c == nil || strings.TrimSpace(c.model.Kind) == "" {
		return DefaultLocalClassifyModel
	}
	return DefaultLocalClassifyModel + "/" + c.model.Kind
}

func (c *LocalFactTypeClassifier) Predict(fact ClassifyableFact) (string, float64) {
	if c == nil {
		return "kv", 0
	}
	if label, confidence, ok := heuristicFactType(fact); ok {
		return label, confidence
	}
	features := c.vectorize(augmentFactTypeText(fact))
	if len(features) == 0 || len(c.model.Classes) == 0 {
		return "kv", 0
	}
	bestLabel := "kv"
	bestProb := 0.0
	for classIdx, className := range c.model.Classes {
		logit := 0.0
		if classIdx < len(c.model.Intercept) {
			logit = c.model.Intercept[classIdx]
		}
		if classIdx < len(c.model.Coef) {
			for idx, value := range features {
				if idx >= 0 && idx < len(c.model.Coef[classIdx]) {
					logit += value * c.model.Coef[classIdx][idx]
				}
			}
		}
		prob := 1.0 / (1.0 + math.Exp(-logit))
		if prob > bestProb {
			bestProb = prob
			bestLabel = className
		}
	}
	if bestLabel != "kv" && bestProb < c.Threshold() {
		return "kv", bestProb
	}
	return bestLabel, bestProb
}

func (c *LocalFactTypeClassifier) vectorize(text string) map[int]float64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if c.model.Lowercase {
		text = strings.ToLower(text)
	}
	tokens := c.tokenRE.FindAllString(text, -1)
	if len(tokens) == 0 {
		return nil
	}
	counts := make(map[int]float64)
	for _, token := range tokens {
		if idx, ok := c.featureIx[token]; ok {
			counts[idx]++
		}
	}
	for i := 0; i < len(tokens)-1; i++ {
		bigram := tokens[i] + " " + tokens[i+1]
		if idx, ok := c.featureIx[bigram]; ok {
			counts[idx]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	norm := 0.0
	for idx, tf := range counts {
		if idx >= len(c.model.IDF) {
			continue
		}
		value := tf * c.model.IDF[idx]
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

func ClassifyFactsLocal(facts []ClassifyableFact, opts ClassifyOpts) (*ClassifyResult, error) {
	classifier, err := NewLocalFactTypeClassifier()
	if err != nil {
		return nil, fmt.Errorf("loading local fact-type classifier: %w", err)
	}
	start := time.Now()
	result := &ClassifyResult{
		TotalFacts: len(facts),
		Model:      classifier.Name(),
		BatchCount: 1,
	}
	minConfidence := classifier.Threshold()
	if opts.MinConfidence > 0 && opts.MinConfidence < minConfidence {
		minConfidence = opts.MinConfidence
	}
	for _, fact := range facts {
		label, confidence := classifier.Predict(fact)
		if !isValidFactType(label) {
			result.Errors++
			continue
		}
		if label == fact.FactType || (label != "kv" && confidence < minConfidence) {
			result.Unchanged++
			continue
		}
		result.Classified = append(result.Classified, FactClassification{
			FactID:     fact.ID,
			OldType:    fact.FactType,
			NewType:    label,
			Confidence: confidence,
		})
	}
	result.Latency = time.Since(start)
	return result, nil
}

func augmentFactTypeText(fact ClassifyableFact) string {
	text := buildFactTypeText(fact)
	tokens := runtimeFactTypeTokens(fact)
	if len(tokens) == 0 {
		return text
	}
	return strings.Join(tokens, " ") + "\n" + text
}

func buildFactTypeText(fact ClassifyableFact) string {
	parts := make([]string, 0, 4)
	if subject := strings.TrimSpace(fact.Subject); subject != "" {
		parts = append(parts, "subject "+subject)
	}
	if predicate := strings.TrimSpace(fact.Predicate); predicate != "" {
		parts = append(parts, "predicate "+predicate)
	}
	if obj := strings.TrimSpace(fact.Object); obj != "" {
		parts = append(parts, "object "+obj)
	}
	return strings.Join(parts, "\n")
}

func runtimeFactTypeTokens(fact ClassifyableFact) []string {
	combined := strings.TrimSpace(buildFactTypeText(fact))
	predicate := strings.TrimSpace(fact.Predicate)
	object := strings.TrimSpace(fact.Object)
	tokens := make([]string, 0, 16)
	if pred := sanitizeFactTypeToken(predicate); pred != "" {
		tokens = append(tokens, "meta_predicate_"+pred)
	}
	if localDateRE.MatchString(combined) || localTimeRE.MatchString(combined) || localTemporalLexRE.MatchString(combined) {
		tokens = append(tokens, "meta_temporal_like")
	}
	if localPathRE.MatchString(combined) {
		tokens = append(tokens, "meta_path_like")
	}
	if localCommitRE.MatchString(combined) {
		tokens = append(tokens, "meta_commit_like")
	}
	if localEnvRE.MatchString(combined) {
		tokens = append(tokens, "meta_env_like")
	}
	if localNumericRE.MatchString(object) {
		tokens = append(tokens, "meta_numeric_object")
	}
	if localEventLexRE.MatchString(combined) {
		tokens = append(tokens, "meta_event_lex")
	}
	if localRuleLexRE.MatchString(combined) {
		tokens = append(tokens, "meta_rule_lex")
	}
	if localDecisionLexRE.MatchString(combined) {
		tokens = append(tokens, "meta_decision_lex")
	}
	if localConfigLexRE.MatchString(combined) {
		tokens = append(tokens, "meta_config_lex")
	}
	if localRelationshipRE.MatchString(combined) {
		tokens = append(tokens, "meta_relationship_lex")
	}
	if localIdentityRE.MatchString(combined) {
		tokens = append(tokens, "meta_identity_lex")
	}
	if localPreferenceRE.MatchString(combined) {
		tokens = append(tokens, "meta_preference_lex")
	}
	if localLocationRE.MatchString(combined) {
		tokens = append(tokens, "meta_location_lex")
	}
	if len(object) <= 20 && object != "" {
		tokens = append(tokens, "meta_object_short")
	}
	if len(object) >= 80 {
		tokens = append(tokens, "meta_object_long")
	}
	return tokens
}

func sanitizeFactTypeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	out = strings.ReplaceAll(out, "__", "_")
	return out
}

func heuristicFactType(fact ClassifyableFact) (string, float64, bool) {
	combined := strings.ToLower(strings.TrimSpace(buildFactTypeText(fact)))
	predicate := strings.ToLower(strings.TrimSpace(fact.Predicate))
	object := strings.ToLower(strings.TrimSpace(fact.Object))

	switch {
	case strings.Contains(predicate, "operational rule"), localRuleLexRE.MatchString(combined) && (strings.Contains(combined, "always") || strings.Contains(combined, "must") || strings.Contains(combined, "constraint") || strings.Contains(combined, "threshold")):
		return "rule", 0.95, true
	case localIdentityRE.MatchString(combined) && (strings.Contains(predicate, "email") || strings.Contains(combined, "@") || strings.Contains(combined, "hostname") || strings.Contains(combined, "user ")):
		return "identity", 0.95, true
	case localDateRE.MatchString(combined) || localTimeRE.MatchString(combined) || (localTemporalLexRE.MatchString(combined) && strings.Contains(object, "2026")):
		return "temporal", 0.94, true
	case strings.Contains(predicate, "branch") || strings.Contains(predicate, "repo") || strings.Contains(predicate, "root") || strings.Contains(predicate, "path") || strings.Contains(combined, "source_file") || (localPathRE.MatchString(combined) && localLocationRE.MatchString(combined)):
		return "location", 0.94, true
	case strings.Contains(predicate, "decision") || localDecisionLexRE.MatchString(combined) && (strings.Contains(combined, "approved") || strings.Contains(combined, "confirmed") || strings.Contains(combined, "q asked")):
		return "decision", 0.93, true
	case strings.Contains(predicate, "status") || strings.Contains(object, "not filed yet") || localStateLexRE.MatchString(combined) && !localEventLexRE.MatchString(combined):
		return "state", 0.92, true
	case localEventLexRE.MatchString(combined) && !strings.Contains(predicate, "status"):
		return "event", 0.91, true
	case localConfigLexRE.MatchString(combined):
		return "config", 0.9, true
	case localRelationshipRE.MatchString(combined):
		return "relationship", 0.89, true
	case localPreferenceRE.MatchString(combined):
		return "preference", 0.88, true
	}
	return "", 0, false
}
