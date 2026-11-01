package observedlimits

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

func drifted(t *testing.T) plans.TierDrift {
	t.Helper()
	return plans.TierDrift{Provider: "anthropic", Window: "weekly", ModelFamily: "sonnet", Drifted: true,
		Lower:        plans.DriftTier{Plan: "claude-pro", Display: "Pro", Windows: 5, Factor: 1},
		Higher:       plans.DriftTier{Plan: "claude-max", Display: "Max 5x", Windows: 6, Factor: 5},
		CatalogRatio: 5, ImpliedRatio: 3}
}

// The overlay round-trips; reset turns it off and empty, and keeps it so
// through an update; turned on, an update fills it again. An unreadable
// file is an error, never an empty overlay to write over.
func TestFileLifecycle(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	f := File{Path: filepath.Join(t.TempDir(), "observed-limits.json")}
	if o, err := f.Load(); err != nil || o.Off || len(o.Limits) != 0 {
		t.Fatalf("missing file = %+v, %v", o, err)
	}
	steps := []struct {
		name    string
		do      func() (plans.ObservedLimits, error)
		off     bool
		nLimits int
	}{
		{"update", func() (plans.ObservedLimits, error) {
			return f.Update(map[string][]plans.TierDrift{"anthropic": {drifted(t)}}, now)
		}, false, 1},
		{"reset", func() (plans.ObservedLimits, error) { return f.Reset(now) }, true, 0},
		{"update while off", func() (plans.ObservedLimits, error) {
			return f.Update(map[string][]plans.TierDrift{"anthropic": {drifted(t)}}, now)
		}, true, 0},
		{"on", func() (plans.ObservedLimits, error) { return f.TurnOn(now) }, false, 0},
		{"update once on", func() (plans.ObservedLimits, error) {
			return f.Update(map[string][]plans.TierDrift{"anthropic": {drifted(t)}}, now)
		}, false, 1},
	}
	for _, s := range steps {
		got, err := s.do()
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		loaded, err := f.Load()
		if err != nil || got.Off != s.off || loaded.Off != s.off || len(loaded.Limits) != s.nLimits {
			t.Fatalf("%s: got %+v, loaded %+v, %v", s.name, got, loaded, err)
		}
	}
	if err := os.WriteFile(f.Path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Load(); err == nil {
		t.Error("an unreadable overlay loaded as empty")
	}
	if _, err := f.Update(map[string][]plans.TierDrift{"anthropic": {drifted(t)}}, now); err == nil {
		t.Error("an update wrote over an unreadable overlay")
	}
}
