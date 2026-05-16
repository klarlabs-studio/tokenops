package prompts

import (
	"strings"
	"testing"
	"time"
)

// Empty input must not panic and must surface a clear recommendation.
func TestAnalyzeEmpty(t *testing.T) {
	f := Analyze(nil)
	if f.TotalPrompts != 0 {
		t.Errorf("total = %d", f.TotalPrompts)
	}
	if len(f.Recommendations) == 0 {
		t.Error("recommendations should never be empty")
	}
}

// Ack-heavy input fires the confirmation-loop recommendation.
func TestAnalyzeAckHeavy(t *testing.T) {
	prompts := []UserPrompt{}
	for i := 0; i < 20; i++ {
		prompts = append(prompts, UserPrompt{Text: "yes"})
	}
	for i := 0; i < 5; i++ {
		prompts = append(prompts, UserPrompt{Text: "implement the authentication middleware"})
	}
	f := Analyze(prompts)
	if f.Acknowledgements != 20 {
		t.Errorf("acks = %d; want 20", f.Acknowledgements)
	}
	if !hasRec(f.Recommendations, "confirmation loops") {
		t.Errorf("missing confirmation-loop rec: %v", f.Recommendations)
	}
}

// Short-prompt-heavy input fires the front-load-context rec.
func TestAnalyzeShortHeavy(t *testing.T) {
	prompts := []UserPrompt{}
	for i := 0; i < 30; i++ {
		prompts = append(prompts, UserPrompt{Text: "do it"})
	}
	prompts = append(prompts, UserPrompt{Text: "implement the authentication middleware with rate limiting and audit logging"})
	f := Analyze(prompts)
	if f.LengthDistribution["<5w"] < 25 {
		t.Errorf("<5w bucket = %d; want >=25", f.LengthDistribution["<5w"])
	}
	if !hasRec(f.Recommendations, "Front-load") {
		t.Errorf("missing front-load rec: %v", f.Recommendations)
	}
}

// Repeated prompts populate RepeatedPrompts and trigger the rec.
func TestAnalyzeRepeats(t *testing.T) {
	prompts := []UserPrompt{}
	for i := 0; i < 5; i++ {
		prompts = append(prompts, UserPrompt{Text: "merge the PR"})
	}
	prompts = append(prompts, UserPrompt{Text: "explain the architecture diagram"})
	f := Analyze(prompts)
	if len(f.RepeatedPrompts) == 0 {
		t.Fatal("expected at least one repeated prompt")
	}
	if f.RepeatedPrompts[0].Count != 5 {
		t.Errorf("top repeat count = %d", f.RepeatedPrompts[0].Count)
	}
	if !hasRec(f.Recommendations, "repeat the same prompt") {
		t.Errorf("missing repeat rec: %v", f.Recommendations)
	}
}

// Length-distribution buckets are exhaustive and add up to total.
func TestAnalyzeBucketingAddsUp(t *testing.T) {
	prompts := []UserPrompt{
		{Text: "go"},
		{Text: "fix the auth middleware in user.go"},
		{Text: strings.Repeat("long prompt about architecture and goals ", 6)},
		{Text: strings.Repeat("very long context dump ", 50)},
	}
	f := Analyze(prompts)
	total := 0
	for _, n := range f.LengthDistribution {
		total += n
	}
	if total != len(prompts) {
		t.Errorf("buckets sum = %d; want %d", total, len(prompts))
	}
}

// Balanced healthy input — no flags fire, recommendation is the
// "looks healthy" line.
func TestAnalyzeBalancedHealthy(t *testing.T) {
	prompts := []UserPrompt{
		{Text: "Refactor pkg/x/handler.go to extract the retry loop into a helper. Keep the existing behavior."},
		{Text: "Add a table-driven test that covers the 429 + 503 retry cases plus the give-up path."},
		{Text: "Update CHANGELOG.md with a 'Changed' entry summarising the refactor and the new tests."},
	}
	f := Analyze(prompts)
	if hasRec(f.Recommendations, "confirmation") || hasRec(f.Recommendations, "Front-load") {
		t.Errorf("unexpected recs for healthy input: %v", f.Recommendations)
	}
	if !hasRec(f.Recommendations, "healthy") {
		t.Errorf("expected 'healthy' rec for balanced input: %v", f.Recommendations)
	}
}

// Timestamps round-trip through the struct so extractor → analyzer
// preserves chronological ordering callers may depend on.
func TestUserPromptTimestamp(t *testing.T) {
	ts := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	p := UserPrompt{Timestamp: ts, Text: "x"}
	if !p.Timestamp.Equal(ts) {
		t.Errorf("ts mismatch")
	}
}

func hasRec(recs []string, fragment string) bool {
	for _, r := range recs {
		if strings.Contains(r, fragment) {
			return true
		}
	}
	return false
}
