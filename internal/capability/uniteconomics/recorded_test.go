package uniteconomics

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// ReadRecorded through the daemon's event cache answers what it does
// reading the store, as readings and outcomes arrive and change.
func TestReadRecordedThroughTheEventCache(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "events.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	rng := rand.New(rand.NewSource(5))
	add := func(prefix string, n int) {
		t.Helper()
		var envs []*eventschema.Envelope
		for i := range n {
			at := now.Add(-time.Duration(rng.Int63n(int64(31 * 24 * time.Hour))))
			reset := at.Truncate(7 * 24 * time.Hour).Add(7 * 24 * time.Hour)
			id := fmt.Sprintf("%s%04d", prefix, i)
			switch rng.Intn(4) {
			case 0:
				envs = append(envs, &eventschema.Envelope{ID: id, SchemaVersion: eventschema.SchemaVersion,
					Type: eventschema.EventTypeOutcome, Timestamp: at, Association: eventschema.Association{Execution: "x" + id},
					Payload: &eventschema.OutcomeEvent{Result: eventschema.OutcomeAchieved, Assessment: eventschema.OutcomeHuman}})
			default:
				e := codexReading(id, at, reset, float64(rng.Intn(100)))
				e.Attributes["sequence"], e.Attributes["service_tier"] = "1", "flex"
				envs = append(envs, e)
			}
		}
		if err := store.AppendBatch(ctx, envs); err != nil {
			t.Fatal(err)
		}
	}
	add("a", 400)
	cache := sqlite.NewEventCache(store, 0, time.Hour)
	recorded := sqlite.NewRecordedEvents(cache)
	start, until := now.Add(-30*24*time.Hour), now.Add(-time.Hour)
	check := func(step string) {
		t.Helper()
		wantOut, wantWin, err := ReadRecorded(ctx, store, start, until)
		if err != nil {
			t.Fatal(err)
		}
		gotOut, gotWin, err := ReadRecorded(ctx, recorded, start, until)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotOut, wantOut) || !reflect.DeepEqual(gotWin, wantWin) {
			t.Fatalf("%s: through the cache %d outcomes, windows %v; store %d, %v", step, len(gotOut), gotWin, len(wantOut), wantWin)
		}
		if len(wantWin) == 0 {
			t.Fatalf("%s: the test reads no windows", step)
		}
	}
	check("first read")
	add("b", 50)
	check("after new readings")
	if _, err := store.Reattribute(ctx, "codex-jsonl", "gpt-5", "openai", "openrouter", "", start, now, false); err != nil {
		t.Fatal(err)
	}
	check("after a re-attribution")
}
