package verify_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/reconstruct"
	"go.klarlabs.de/tokenops/internal/capability/verify"
	"go.klarlabs.de/tokenops/internal/contexts/work"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// storeWith opens a store in a temp dir holding events.
func storeWith(t *testing.T, events []*eventschema.Envelope) *sqlite.Store {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.AppendBatch(ctx, events); err != nil {
		t.Fatal(err)
	}
	return store
}

func reconstructed(execs []work.Execution) []reconstruct.Work {
	rs := make([]reconstruct.Work, 0, len(execs))
	for _, e := range execs {
		rs = append(rs, reconstruct.Work{Execution: e})
	}
	return rs
}

// noise is n prompt events from another session, recorded from start on:
// what a busy machine records around the attempts being compared.
func noise(start time.Time, n int) []*eventschema.Envelope {
	out := make([]*eventschema.Envelope, 0, n)
	for i := range n {
		env := promptEvent("session:noise", start.Add(time.Duration(i)*time.Millisecond), 10)
		env.ID = fmt.Sprintf("noise-%07d", i)
		out = append(out, env)
	}
	return out
}

// A busy store holds more events in a period than verify used to read
// (200,000, keeping the newest), and more than the store reads in one
// page. The attempts at the start of the period were then compared
// against none of their events and reported as unknown, with nothing
// saying the read stopped short. Every event in the period is read.
func TestCompareStoredReadsEveryEventInThePeriod(t *testing.T) {
	const busy = 12_000
	baseAt := t0.Add(-24 * time.Hour)
	execs, randomized := randomizedFixtureAt(baseAt, 3, "experiment:early", "early")
	single := execution("e1", "session:a", baseAt, time.Hour)
	tests := []struct {
		name   string
		execs  []work.Execution
		events []*eventschema.Envelope
		check  func(*testing.T, verify.Report)
	}{
		{
			name:   "an attempt before the busiest stretch keeps its consumption",
			execs:  []work.Execution{single},
			events: []*eventschema.Envelope{promptEvent("session:a", baseAt.Add(time.Minute), 100)},
			check: func(t *testing.T, r verify.Report) {
				if len(r.Attributed) != 1 {
					t.Fatalf("attributed %d attempts, want 1", len(r.Attributed))
				}
				if got := r.Attributed[0].Tokens.AmountOr(-1); got != 100 {
					t.Errorf("tokens = %v, want 100", got)
				}
			},
		},
		{
			name:   "a randomized trial before the busiest stretch is still compared",
			execs:  execs,
			events: randomized,
			check: func(t *testing.T, r verify.Report) {
				if r.RandomizedExperimentID != "experiment:early" || r.RandomizedPairs != 3 {
					t.Errorf("randomized = %q with %d pairs (fallback %q), want experiment:early with 3",
						r.RandomizedExperimentID, r.RandomizedPairs, r.RandomizedFallbackReason)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events := append(append([]*eventschema.Envelope{}, tc.events...), noise(t0, busy)...)
			store := storeWith(t, events)
			got, err := verify.CompareStored(context.Background(), store, reconstructed(tc.execs), baseAt.Add(-time.Hour), "")
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, got)
		})
	}
}

// Reading from the store keeps only the events that can bear on the
// comparison. Whatever it leaves out must not change the report: it is
// the report CompareReconstructedExperiment gives over every event.
func TestCompareStoredMatchesTheComparisonOverEveryEvent(t *testing.T) {
	execs, randomized := randomizedFixture(3, "experiment:test", "test")

	unlinked := experimentAssignment("", "experiment:legacy", "baseline", 1, t0.Add(30*time.Minute))
	unlinked.ID = "assignment-unlinked"

	proxyID := "proxy:variant"
	proxyPrompt := promptEvent("", t0.Add(time.Hour-time.Millisecond), 70)
	proxyPrompt.Association.Execution = proxyID
	proxyPrompt.Correlation.Experiment = "experiment:proxy"

	late := outcomeEvent("e1", eventschema.OutcomeAchieved, t0.Add(48*time.Hour))
	outside := promptEvent("session:a", t0.Add(3*time.Hour), 999)
	running := work.Attempt("e2", "w-e2", "session:b", t0.Add(4*time.Hour))
	tests := []struct {
		name         string
		execs        []work.Execution
		events       []*eventschema.Envelope
		experimentID string
	}{
		{name: "randomized trial", execs: execs, events: randomized},
		{name: "randomized trial named", execs: execs, events: randomized, experimentID: "experiment:test"},
		{name: "unlinked assignment of another trial", execs: execs, events: append(append([]*eventschema.Envelope{}, randomized...), unlinked)},
		{
			name:  "observational attempts with late outcome, other actors and a running attempt",
			execs: []work.Execution{execution("e1", "session:a", t0, time.Hour), running},
			events: []*eventschema.Envelope{
				promptEvent("session:a", t0.Add(time.Minute), 100),
				promptEvent("session:b", t0.Add(time.Minute), 900),
				promptEvent("session:b", t0.Add(5*time.Hour), 40),
				outside, late,
			},
		},
		{
			name:         "proxy execution known only by its assignment",
			events:       []*eventschema.Envelope{experimentAssignment(proxyID, "experiment:proxy", "variant", 1, t0.Add(time.Hour)), proxyPrompt},
			experimentID: "experiment:proxy",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events := append(append([]*eventschema.Envelope{}, tc.events...), noise(t0.Add(-time.Hour), 50)...)
			store := storeWith(t, events)
			stored, err := store.Query(context.Background(), sqlite.Filter{Limit: len(events)})
			if err != nil {
				t.Fatal(err)
			}
			want := verify.CompareReconstructedExperiment(reconstructed(tc.execs), stored, tc.experimentID)
			got, err := verify.CompareStored(context.Background(), store, reconstructed(tc.execs), time.Time{}, tc.experimentID)
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("report from the store differs from the report over every event\n got: %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}
