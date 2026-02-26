package graph

import "testing"

func requireMetaInt(t *testing.T, meta map[string]interface{}, key string) int {
	t.Helper()
	raw, ok := meta[key]
	if !ok {
		t.Fatalf("expected meta[%q] to exist", key)
	}
	switch v := raw.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		t.Fatalf("expected numeric meta[%q], got %T", key, raw)
		return 0
	}
}

func requireMetaBool(t *testing.T, meta map[string]interface{}, key string) bool {
	t.Helper()
	raw, ok := meta[key]
	if !ok {
		t.Fatalf("expected meta[%q] to exist", key)
	}
	b, ok := raw.(bool)
	if !ok {
		t.Fatalf("expected bool meta[%q], got %T", key, raw)
	}
	return b
}
