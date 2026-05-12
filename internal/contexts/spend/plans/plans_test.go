package plans

import (
	"strings"
	"testing"
)

func TestCatalogEntriesHaveProviderAndSource(t *testing.T) {
	for _, name := range Names() {
		p, ok := Lookup(name)
		if !ok {
			t.Fatalf("Names() returned %q but Lookup missed it", name)
		}
		if p.Provider == "" {
			t.Errorf("%s: empty Provider", name)
		}
		if p.Display == "" {
			t.Errorf("%s: empty Display", name)
		}
		if !strings.Contains(p.SourceURL, "://") {
			t.Errorf("%s: SourceURL missing scheme: %q", name, p.SourceURL)
		}
		if !strings.Contains(p.SourceURL, "(2026-") {
			t.Errorf("%s: SourceURL missing dated snapshot marker: %q", name, p.SourceURL)
		}
		// Window/cap/unit must be coherent: either all three set or
		// MessagesPerWindow zero (meaning "vendor publishes no number,
		// rate-limit window only").
		if p.MessagesPerWindow > 0 {
			if p.RateLimitWindow <= 0 {
				t.Errorf("%s: MessagesPerWindow=%d but RateLimitWindow is zero", name, p.MessagesPerWindow)
			}
			if p.WindowUnit == "" {
				t.Errorf("%s: MessagesPerWindow set but WindowUnit empty", name)
			}
		}
	}
}

func TestCatalogCoversPublishedPlans(t *testing.T) {
	// Test pins the headline plans the docs reference. Adding a new
	// plan to the catalog is fine; removing one of these requires
	// touching this test and the README in the same PR so users don't
	// silently lose support.
	want := []string{
		"claude-max-5x", "claude-max-20x", "claude-pro",
		"claude-code-max", "claude-code-pro",
		"gpt-plus", "gpt-pro", "gpt-team",
		"copilot-individual", "copilot-business",
		"cursor-pro", "cursor-business",
	}
	for _, w := range want {
		if _, ok := Lookup(w); !ok {
			t.Errorf("catalog missing required plan %q", w)
		}
	}
}

func TestValidateRejectsUnknownWithSuggestions(t *testing.T) {
	err := Validate("claude-maxx")
	if err == nil {
		t.Fatal("expected error for typo")
	}
	if !strings.Contains(err.Error(), "claude-max-5x") {
		t.Errorf("error should list valid plans, got: %v", err)
	}
}

func TestValidateAcceptsKnown(t *testing.T) {
	if err := Validate("claude-max-20x"); err != nil {
		t.Errorf("Validate(claude-max-20x) returned %v", err)
	}
}
