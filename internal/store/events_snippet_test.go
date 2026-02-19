package store

import (
	"strings"
	"testing"
)

func TestExtractSnippetUnicodeBoundaries(t *testing.T) {
	content := "これはテストです。Cortex はメモリ検索をします。さらに日本語の文を追加します。"
	snippet := extractSnippet(content, "メモリ")

	if strings.ContainsRune(snippet, '\ufffd') {
		t.Fatalf("snippet contains replacement character: %q", snippet)
	}
	if !strings.Contains(snippet, "メモリ") {
		t.Fatalf("snippet should include query term, got: %q", snippet)
	}
}
