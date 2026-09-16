package coachhook

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// ocStore builds a minimal opencode message store.
func ocStore(t *testing.T, rows ...map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		b, _ := json.Marshal(r)
		if _, err := db.Exec(`INSERT INTO message (id, session_id, data) VALUES (?, ?, ?)`,
			"m"+itoa(int64(i)), "s1", string(b)); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func ocTurn(model string, cost float64, in, out, cacheRead int) map[string]any {
	return map[string]any{
		"role": "assistant", "providerID": "openai", "modelID": model, "cost": cost,
		"tokens": map[string]any{
			"input": in, "output": out, "reasoning": 0,
			"cache": map[string]any{"read": cacheRead, "write": 0},
		},
	}
}

// opencode is the one client that already knows what its turns cost, so
// the coach uses that figure rather than inferring one — which is how a
// model no rate card carries still produces a number.
func TestOpencodePrefersItsOwnCost(t *testing.T) {
	db := ocStore(t,
		ocTurn("a-model-no-card-knows", 1.25, 1000, 100, 0),
		ocTurn("a-model-no-card-knows", 2.50, 2000, 200, 0),
	)
	dec := EvaluateOpencode(t.TempDir(), db, "s1", DefaultConfig(), fixedNow)
	if dec.CumulativeUSD < 3.74 || dec.CumulativeUSD > 3.76 {
		t.Errorf("CumulativeUSD = %v, want 3.75 from opencode's own figures", dec.CumulativeUSD)
	}
	if dec.UnpricedModel != "" {
		t.Errorf("UnpricedModel = %q, but opencode priced every turn", dec.UnpricedModel)
	}
}

// A zero cost is usually CORRECT — a subscription-covered turn costs
// nothing at the margin. But the budget is denominated in API-equivalent
// spend, the counterfactual, so a zero falls back to the rate card
// exactly as it does for Claude Code on a subscription.
func TestOpencodeFallsBackToTheCardOnAZeroCost(t *testing.T) {
	db := ocStore(t, ocTurn("gpt-4o", 0, 1_000_000, 0, 0))
	dec := EvaluateOpencode(t.TempDir(), db, "s1", DefaultConfig(), fixedNow)
	if dec.CumulativeUSD <= 0 {
		t.Error("a subscription-covered turn produced $0; the budget measures the counterfactual")
	}
	if dec.UnpricedModel != "" {
		t.Errorf("UnpricedModel = %q for a model the card knows", dec.UnpricedModel)
	}
}

// Neither source can answer: say so rather than reporting a free session.
func TestOpencodeReportsAZeroItCannotExplain(t *testing.T) {
	db := ocStore(t, ocTurn("big-pickle", 0, 1_000_000, 0, 0))
	dec := EvaluateOpencode(t.TempDir(), db, "s1", DefaultConfig(), fixedNow)
	if dec.UnpricedModel != "big-pickle" {
		t.Errorf("UnpricedModel = %q, want big-pickle — a session nobody could price is not a free one", dec.UnpricedModel)
	}
}

// session.idle fires every time the operator stops typing. Recomputing
// the whole session makes that idempotent; accumulating would inflate it
// without bound.
func TestOpencodeIsIdempotentAcrossRepeatedIdles(t *testing.T) {
	dir := t.TempDir()
	db := ocStore(t, ocTurn("a-model", 5.00, 1000, 100, 0))
	first := EvaluateOpencode(dir, db, "s1", DefaultConfig(), fixedNow)
	for i := range 4 {
		got := EvaluateOpencode(dir, db, "s1", DefaultConfig(), fixedNow.Add(time.Duration(i)*time.Minute))
		if got.CumulativeUSD != first.CumulativeUSD {
			t.Fatalf("idle %d moved cumulative %v -> %v", i+2, first.CumulativeUSD, got.CumulativeUSD)
		}
	}
}

// The tier must latch, or the nudge repeats on every idle — and opencode
// goes idle constantly. This failed for real until the state directory
// was created: the writes went nowhere and nothing was ever remembered.
func TestOpencodeLatchesTheTierAcrossIdles(t *testing.T) {
	dir := t.TempDir()
	db := ocStore(t, ocTurn("a-model", 100.00, 1000, 100, 0))
	cfg := DefaultConfig()
	if first := EvaluateOpencode(dir, db, "s1", cfg, fixedNow); !first.Nudge {
		t.Fatal("no nudge at 200% of budget")
	}
	if again := EvaluateOpencode(dir, db, "s1", cfg, fixedNow.Add(time.Minute)); again.Nudge {
		t.Errorf("nudged twice for one tier: %q", again.Message)
	}
}

// One session's turns, not the whole store.
func TestOpencodeScopesToTheSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i, s := range []string{"s1", "s2"} {
		b, _ := json.Marshal(ocTurn("a-model", 10, 1000, 10, 0))
		if _, err := db.Exec(`INSERT INTO message VALUES (?, ?, ?)`, "m"+itoa(int64(i)), s, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	dec := EvaluateOpencode(t.TempDir(), path, "s1", DefaultConfig(), fixedNow)
	if dec.CumulativeUSD != 10 {
		t.Errorf("CumulativeUSD = %v, want 10 — the other session's turn leaked in", dec.CumulativeUSD)
	}
}
