package uniteconomics

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// codexReading is a Codex window reading: its weekly window resetting at
// reset, used to pct.
func codexReading(id string, at, reset time.Time, pct float64) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: at, Source: "codex-jsonl",
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderOpenAI},
		Attributes: map[string]string{
			"primary_used_pct":   strconv.FormatFloat(pct, 'f', -1, 64),
			"primary_window_min": strconv.Itoa(7 * 24 * 60),
			"primary_resets_at":  strconv.FormatInt(reset.Unix(), 10),
		},
	}
}

// A busy machine records tens of thousands of window readings and
// outcomes in the baseline, more than a capped read returned (50,000
// each), and more than the store reads in one page. Every one is read:
// the oldest window and the oldest outcome are what the newest rows used
// to push out.
func TestGatherReadsEveryRecordedEvent(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	start, since := t0.Add(-28*day), t0
	until := t0.Add(7 * day)
	oldReset := start.Add(3 * day).Truncate(time.Second)
	newReset := until.Add(day).Truncate(time.Second)
	const busy = 6_000
	envs := make([]*eventschema.Envelope, 0, 2+2*busy)
	envs = append(envs,
		codexReading("old", start.Add(time.Hour), oldReset, 90),
		&eventschema.Envelope{ID: "old-outcome", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypeOutcome,
			Timestamp: start.Add(time.Hour), Association: eventschema.Association{Execution: "x0"},
			Payload: &eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman}})
	step := (until.Sub(start) - 2*time.Hour) / busy
	for i := range busy {
		at := start.Add(2*time.Hour + time.Duration(i)*step)
		envs = append(envs, codexReading(fmt.Sprintf("r%06d", i), at, newReset, float64(i%100)))
		envs = append(envs, &eventschema.Envelope{ID: fmt.Sprintf("o%06d", i), SchemaVersion: eventschema.SchemaVersion,
			Type: eventschema.EventTypeOutcome, Timestamp: at, Association: eventschema.Association{Execution: fmt.Sprintf("x%d", i+1)},
			Payload: &eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman}})
	}
	if err := store.AppendBatch(ctx, envs); err != nil {
		t.Fatal(err)
	}

	d := Deps{
		Turns:  func(context.Context, time.Time) ([]analytics.SessionTurn, error) { return nil, nil },
		Dirs:   func(time.Time) map[string]sessiondirs.Dir { return nil },
		Events: store,
	}
	in, err := Gather(ctx, d, start, since, until)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(in.Outcomes), busy+1; got != want {
		t.Errorf("read %d outcomes, want %d", got, want)
	}
	if len(in.Outcomes) > 0 && in.Outcomes[0].ID != "old-outcome" {
		t.Errorf("oldest outcome is %s, want old-outcome", in.Outcomes[0].ID)
	}
	peaks := map[time.Time]float64{}
	for _, w := range in.Windows["openai"] {
		peaks[w.ResetsAt] = max(peaks[w.ResetsAt], w.UsedPct)
	}
	if peaks[oldReset.UTC()] != 90 {
		t.Errorf("oldest window's peak = %v, want 90 (readings: %v)", peaks[oldReset.UTC()], peaks)
	}
	if peaks[newReset.UTC()] != 99 {
		t.Errorf("newest window's peak = %v, want 99", peaks[newReset.UTC()])
	}
}
