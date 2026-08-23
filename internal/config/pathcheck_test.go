package config

import (
	"path/filepath"
	"slices"
	"testing"
)

// A rules.root pointing at a directory that no longer exists is a silent
// failure: rule intelligence keeps analysing nothing and reports success.
// On the maintainer's own machine the repo had moved months earlier and
// nothing ever said so.
func TestMissingRulesRootIsABlocker(t *testing.T) {
	c := Config{
		Storage: StorageConfig{Enabled: true},
		Rules:   RulesConfig{Enabled: true, Root: filepath.Join(t.TempDir(), "gone")},
	}
	got := c.Blockers()
	if !slices.Contains(got, "rules_root_missing") {
		t.Errorf("blockers = %v, want rules_root_missing", got)
	}
	actions := NextActionsFor(got)
	var mentioned bool
	for _, a := range actions {
		if len(a) > 0 && (containsAll(a, "rules.root")) {
			mentioned = true
		}
	}
	if !mentioned {
		t.Errorf("next actions should name rules.root, got %v", actions)
	}
}

// An existing root is fine.
func TestPresentRulesRootIsNotABlocker(t *testing.T) {
	c := Config{
		Storage: StorageConfig{Enabled: true},
		Rules:   RulesConfig{Enabled: true, Root: t.TempDir()},
	}
	if slices.Contains(c.Blockers(), "rules_root_missing") {
		t.Errorf("existing root flagged as missing")
	}
}

// An empty root means "use the working directory" and must not trip.
func TestEmptyRulesRootIsNotABlocker(t *testing.T) {
	c := Config{
		Storage: StorageConfig{Enabled: true},
		Rules:   RulesConfig{Enabled: true},
	}
	if slices.Contains(c.Blockers(), "rules_root_missing") {
		t.Errorf("empty root (defaults to cwd) flagged as missing")
	}
}

// Rules being off entirely is already its own blocker; don't add a
// second one about a path nobody reads.
func TestDisabledRulesDoesNotAddPathBlocker(t *testing.T) {
	c := Config{
		Storage: StorageConfig{Enabled: true},
		Rules:   RulesConfig{Enabled: false, Root: filepath.Join(t.TempDir(), "gone")},
	}
	if slices.Contains(c.Blockers(), "rules_root_missing") {
		t.Errorf("disabled rules should not report a missing root")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
