package domainevents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplayLenientReadsLegacyJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.jsonl")
	contents := "{\"kind\":\"budget.exceeded\",\"at\":\"2026-09-24T12:00:00Z\",\"payload\":{\"BudgetID\":\"daily\"}}\nnot-json\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []Record
	skipped, err := ReplayLenient(path, func(rec Record) error {
		got = append(got, rec)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || len(got) != 1 || got[0].Kind != KindBudgetExceeded {
		t.Fatalf("skipped=%d records=%+v", skipped, got)
	}
}

func TestReplayMissingLegacyFileIsEmpty(t *testing.T) {
	if err := Replay(filepath.Join(t.TempDir(), "missing.jsonl"), func(Record) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
