package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// `tokenops outcome record` runs at the operator's terminal: it records
// their judgement as human, keeps their attention minutes, and names the
// channel on the event.
func TestOutcomeRecordIsTheOperatorsJudgement(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "events.db")
	store, err := sqlite.Open(ctx, db, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	cmd := newOutcomeRecordCmd()
	cmd.SetContext(ctx)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"exec-1", "--result", "partial", "--attention-minutes", "4", "--db", db})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	store, err = sqlite.Open(ctx, db, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	events, err := store.Query(ctx, sqlite.Filter{Type: eventschema.EventTypeOutcome})
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %d, %v", len(events), err)
	}
	out := events[0].Payload.(*eventschema.OutcomeEvent)
	if out.Assessment != eventschema.OutcomeHuman || out.Result != eventschema.OutcomePartial {
		t.Fatalf("outcome = %+v", out)
	}
	if len(out.Metrics) != 1 || out.Metrics[0].Value != 4 {
		t.Fatalf("attention = %+v", out.Metrics)
	}
	if len(out.Evidence) != 1 || out.Evidence[0].Kind != outcomes.ReportedOutcomeKind || out.Evidence[0].Source != string(outcomes.ViaCLI) {
		t.Fatalf("evidence = %+v", out.Evidence)
	}
}
