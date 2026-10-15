package providers

import (
	"slices"
	"testing"
)

func TestResolveAcceptsAnIDOrAName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"fireworks", "fireworks"},
		{"  Fireworks ", "fireworks"},
		{"FIREWORKS", "fireworks"},
		{"anthropic", "anthropic"},
		{"openrouter", "openrouter"},
		{"OpenRouter", "openrouter"},
		{"open-router", "openrouter"},
	} {
		got, ok, _ := Resolve(tc.in)
		if !ok || got != tc.want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", tc.in, got, ok, tc.want)
		}
	}
}

func TestResolveRefusesAnUnknownIDWithSuggestions(t *testing.T) {
	for _, tc := range []struct{ in, suggested string }{
		{"firework", "fireworks"},
		{"fireworx", "fireworks"},
		{"antropic", "anthropic"},
		{"open-routr", "openrouter"},
	} {
		got, ok, sugg := Resolve(tc.in)
		if ok {
			t.Errorf("Resolve(%q) = %q, want refused", tc.in, got)
			continue
		}
		if !slices.Contains(sugg, tc.suggested) {
			t.Errorf("Resolve(%q) suggests %v, want %q among them", tc.in, sugg, tc.suggested)
		}
		if len(sugg) > maxSuggestions {
			t.Errorf("Resolve(%q) suggests %d ids, want at most %d", tc.in, len(sugg), maxSuggestions)
		}
	}
	if _, ok, sugg := Resolve("zzzzqqqq"); ok || len(sugg) != 0 {
		t.Errorf("nonsense: ok=%v suggestions=%v", ok, sugg)
	}
	if _, ok, _ := Resolve(""); ok {
		t.Error("empty id resolved")
	}
}
