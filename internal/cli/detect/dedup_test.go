package detect

import (
	"path/filepath"
	"testing"
)

// Detect's doc has always promised "the deduplicated set". It never
// deduplicated: ~/.claude plus ANTHROPIC_API_KEY produced two anthropic
// rows, and the operator was shown the same provider twice with different
// advice under each.
func TestDetectDeduplicatesByProvider(t *testing.T) {
	env := fakeEnv{
		home: filepath.FromSlash("/home/x"),
		envs: map[string]string{"ANTHROPIC_API_KEY": "sk-ant-xxx"},
		dirs: map[string]bool{filepath.FromSlash("/home/x/.claude"): true},
	}
	seen := map[string]int{}
	for _, d := range Detect(env) {
		seen[d.Provider]++
	}
	if seen["anthropic"] != 1 {
		t.Fatalf("anthropic detected %d times, want 1", seen["anthropic"])
	}
}

// The installed client is the stronger evidence, so it is the one kept.
func TestDetectKeepsTheHighestConfidenceDetection(t *testing.T) {
	env := fakeEnv{
		home: filepath.FromSlash("/home/x"),
		envs: map[string]string{"ANTHROPIC_API_KEY": "sk-ant-xxx"},
		dirs: map[string]bool{filepath.FromSlash("/home/x/.claude"): true},
	}
	for _, d := range Detect(env) {
		if d.Provider != "anthropic" {
			continue
		}
		if d.Confidence != ConfidenceMedium {
			t.Fatalf("kept the %s detection, want the medium-confidence client: %+v", d.Confidence, d)
		}
	}
}

// A bare API key means metered billing. Telling that operator to bind a Max
// subscription contradicts the hint printed directly above it, and would
// make every headroom figure wrong.
func TestAPIKeyDetectionIsNotAPlan(t *testing.T) {
	env := fakeEnv{
		home: filepath.FromSlash("/home/x"),
		envs: map[string]string{"ANTHROPIC_API_KEY": "sk-ant-xxx"},
	}
	found := false
	for _, d := range Detect(env) {
		if d.Provider == "anthropic" {
			found = true
			if d.SuggestsPlan {
				t.Errorf("an API key must not suggest a subscription plan: %+v", d)
			}
		}
	}
	if !found {
		t.Fatal("the API key should still be reported")
	}
}

// A detected client still suggests a plan — that is the whole point of the
// paste-ready line.
func TestClientDetectionSuggestsAPlan(t *testing.T) {
	env := fakeEnv{home: filepath.FromSlash("/home/x"), dirs: map[string]bool{"/home/x/.claude": true}}
	ds := Detect(env)
	if len(ds) != 1 || !ds[0].SuggestsPlan {
		t.Fatalf("a detected client should suggest a plan: %+v", ds)
	}
}
