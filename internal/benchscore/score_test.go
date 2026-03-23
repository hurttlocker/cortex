package benchscore

import "testing"

func TestNormalizeAnswer(t *testing.T) {
	got := NormalizeAnswer("The June 20, 2023 answer.")
	if got != "june 20 2023 answer" {
		t.Fatalf("NormalizeAnswer = %q", got)
	}
}

func TestNormalizedExactMatch(t *testing.T) {
	if !NormalizedExactMatch("June 20, 2023", "the June 20 2023", "June 21, 2023") {
		t.Fatal("expected normalized exact match")
	}
	if NormalizedExactMatch("June 22, 2023", "the June 20 2023") {
		t.Fatal("did not expect normalized exact match")
	}
}

func TestContainsNormalizedPhrase(t *testing.T) {
	haystack := "Jon said the official opening night is June 20, 2023 in Session 9."
	if !ContainsNormalizedPhrase(haystack, "June 20 2023") {
		t.Fatal("expected normalized phrase containment")
	}
	if ContainsNormalizedPhrase(haystack, "June 21 2023") {
		t.Fatal("did not expect wrong phrase containment")
	}
}

func TestNormalizedAccuracy(t *testing.T) {
	if score := NormalizedAccuracy("Gina", "gina", "jon"); score != 1.0 {
		t.Fatalf("NormalizedAccuracy = %.1f, want 1.0", score)
	}
	if score := NormalizedAccuracy("Jon", "gina"); score != 0.0 {
		t.Fatalf("NormalizedAccuracy = %.1f, want 0.0", score)
	}
}
