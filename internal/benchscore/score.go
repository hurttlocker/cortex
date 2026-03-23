package benchscore

import (
	"regexp"
	"strings"
)

var answerTokenRE = regexp.MustCompile(`[a-z0-9]+`)

// NormalizeAnswer mirrors the lightweight normalization used in the local
// LoCoMo comparison harness: lowercase, remove punctuation, drop articles,
// and collapse whitespace.
func NormalizeAnswer(text string) string {
	text = strings.ReplaceAll(text, ",", "")
	text = strings.ToLower(text)
	text = stripPunctuation(text)
	text = removeArticles(text)
	return strings.Join(strings.Fields(text), " ")
}

// NormalizedExactMatch returns true when the normalized prediction matches any
// normalized gold alias exactly.
func NormalizedExactMatch(prediction string, goldAliases ...string) bool {
	prediction = NormalizeAnswer(prediction)
	if prediction == "" {
		return false
	}
	for _, gold := range goldAliases {
		if prediction == NormalizeAnswer(gold) {
			return true
		}
	}
	return false
}

// ContainsNormalizedPhrase returns true when the normalized gold phrase appears
// as a contiguous token span inside the normalized haystack.
func ContainsNormalizedPhrase(haystack string, phrase string) bool {
	haystackTokens := answerTokenRE.FindAllString(strings.ToLower(NormalizeAnswer(haystack)), -1)
	phraseTokens := answerTokenRE.FindAllString(strings.ToLower(NormalizeAnswer(phrase)), -1)
	if len(haystackTokens) == 0 || len(phraseTokens) == 0 || len(phraseTokens) > len(haystackTokens) {
		return false
	}
	width := len(phraseTokens)
	for idx := 0; idx <= len(haystackTokens)-width; idx++ {
		match := true
		for offset := 0; offset < width; offset++ {
			if haystackTokens[idx+offset] != phraseTokens[offset] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// NormalizedAccuracy returns 1 when the normalized prediction matches any gold
// alias exactly, otherwise 0.
func NormalizedAccuracy(prediction string, goldAliases ...string) float64 {
	if NormalizedExactMatch(prediction, goldAliases...) {
		return 1.0
	}
	return 0.0
}

func stripPunctuation(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '\n' || r == '\t':
			b.WriteRune(' ')
		default:
			// drop punctuation/symbols
		}
	}
	return b.String()
}

func removeArticles(text string) string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	filtered := make([]string, 0, len(words))
	for _, word := range words {
		switch word {
		case "a", "an", "the", "and":
			continue
		default:
			filtered = append(filtered, word)
		}
	}
	return strings.Join(filtered, " ")
}
