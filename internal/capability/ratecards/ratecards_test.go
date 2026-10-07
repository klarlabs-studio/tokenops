package ratecards

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestAssessComparesWithTheLatestStoredCard(t *testing.T) {
	dir := t.TempDir()
	fetched := Snapshot{Source: "litellm", FetchedAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), Rates: map[string]pricing.Rate{
		"openai/gpt-4o": {InputPerMillion: 2.5, OutputPerMillion: 10, CachedInputPerMillion: 5},
	}}
	if got := Assess(dir, fetched); got.PreviousStored || got.Previous.Source != pricing.SourceEmbeddedBaseline {
		t.Errorf("with nothing stored, compared with %q (stored %v), want the baseline", got.Previous.Source, got.PreviousStored)
	}
	stored := fetched
	stored.FetchedAt = fetched.FetchedAt.AddDate(0, 0, -1)
	if _, err := Save(dir, stored); err != nil {
		t.Fatal(err)
	}
	got := Assess(dir, fetched)
	if !got.PreviousStored || len(got.Changes) != 0 {
		t.Errorf("against an identical stored card: stored %v, %d changes", got.PreviousStored, len(got.Changes))
	}
	if len(got.Anomalies) != 1 {
		t.Errorf("%d anomalies, want the cache read dearer than input", len(got.Anomalies))
	}
}

func TestSourceNamedRefusesAnUnknownName(t *testing.T) {
	if _, err := SourceNamed("nope", ""); !errors.Is(err, ErrUnknownSource) {
		t.Errorf("err %v, want ErrUnknownSource", err)
	}
	for _, name := range []string{"", "default", "litellm", "models.dev"} {
		if _, err := SourceNamed(name, ""); err != nil {
			t.Errorf("SourceNamed(%q): %v", name, err)
		}
	}
}

// An override file that cannot be read leaves the card pricing, without it.
func TestTablesSurviveAnUnreadableOverrideFile(t *testing.T) {
	dated := Tables(t.TempDir(), filepath.Join(t.TempDir(), "missing.yaml"))
	if len(dated) == 0 {
		t.Fatal("no tables")
	}
	if _, err := dated[len(dated)-1].Table.Lookup(eventschema.ProviderAnthropic, "claude-opus-4-7"); err != nil {
		t.Errorf("baseline row missing: %v", err)
	}
}
