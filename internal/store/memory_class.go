package store

import (
	"fmt"
	"sort"
	"strings"
)

const (
	MemoryClassRule       = "rule"
	MemoryClassDecision   = "decision"
	MemoryClassPreference = "preference"
	MemoryClassIdentity   = "identity"
	MemoryClassStatus     = "status"
	MemoryClassScratch    = "scratch"
)

var validMemoryClasses = map[string]struct{}{
	MemoryClassRule:       {},
	MemoryClassDecision:   {},
	MemoryClassPreference: {},
	MemoryClassIdentity:   {},
	MemoryClassStatus:     {},
	MemoryClassScratch:    {},
}

// NormalizeMemoryClass trims and lowercases a class label.
func NormalizeMemoryClass(class string) string {
	return strings.ToLower(strings.TrimSpace(class))
}

// IsValidMemoryClass returns true if class is one of Cortex's supported memory classes.
func IsValidMemoryClass(class string) bool {
	_, ok := validMemoryClasses[NormalizeMemoryClass(class)]
	return ok
}

// ParseMemoryClassList parses comma-separated memory classes.
// Returns a normalized, de-duplicated list preserving input order.
func ParseMemoryClassList(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	classes := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))

	for _, p := range parts {
		c := NormalizeMemoryClass(p)
		if c == "" {
			continue
		}
		if !IsValidMemoryClass(c) {
			return nil, fmt.Errorf("invalid memory class %q (valid: %s)", c, strings.Join(AvailableMemoryClasses(), ","))
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		classes = append(classes, c)
	}

	if len(classes) == 0 {
		return nil, nil
	}
	return classes, nil
}

// AvailableMemoryClasses returns supported classes sorted alphabetically.
func AvailableMemoryClasses() []string {
	out := make([]string, 0, len(validMemoryClasses))
	for c := range validMemoryClasses {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
