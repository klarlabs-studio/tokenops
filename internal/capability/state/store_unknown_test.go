package state

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

// slowStore is an event store that missed the assessment's deadline.
type slowStore struct{}

func (slowStore) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return nil, context.DeadlineExceeded
}

func (slowStore) LastEventBySource(context.Context) (map[string]time.Time, error) {
	return nil, context.DeadlineExceeded
}

// A store that did not answer in time is not a store with no events.
// Observed on a loaded machine after a restart: both queries missed the
// deadline, every source was reported never seen with nothing in the
// window, and status warned that Claude Code and Codex had never been
// ingested while the store held thousands of their events.
func TestAssessSourcesDoesNotReadAFailedStoreAsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	t.Setenv("XDG_DATA_HOME", home+"/.local/share")
	t.Setenv("XDG_STATE_HOME", home+"/.local/state")

	cfg := config.Config{}
	cfg.VendorUsage.ClaudeCodeJSONL.Enabled = true
	reports := AssessSources(context.Background(), cfg, slowStore{}, nil, nil, time.Now())
	if len(reports) == 0 {
		t.Fatal("no reports")
	}
	for _, r := range reports {
		if r.NeverSeen {
			t.Errorf("%s: reported never seen on a store that did not answer", r.Tag)
		}
		if !r.EventsUnknown {
			t.Errorf("%s: counts not marked unknown", r.Tag)
		}
		if r.State == freshness.StateStale {
			t.Errorf("%s: graded stale on a store that did not answer", r.Tag)
		}
	}
}

// A stale source whose last event the store could not date is warned
// about without the claim that nothing was ever ingested from it.
func TestSourceWarningsDoNotSayNeverWhenTheLastEventIsUnknown(t *testing.T) {
	got := SourceWarnings([]SourceReport{{
		Tag: "codex-jsonl", State: freshness.StateStale, Severity: freshness.SeverityWarning,
		Window: 48 * time.Hour,
	}})
	if len(got.Stale) != 1 {
		t.Fatalf("stale warnings %v, want 1", got.Stale)
	}
	if strings.Contains(got.Stale[0], "never") {
		t.Errorf("warning %q claims the source was never ingested", got.Stale[0])
	}
	if !strings.Contains(got.Stale[0], "48h") {
		t.Errorf("warning %q does not name the window it checked", got.Stale[0])
	}
}
