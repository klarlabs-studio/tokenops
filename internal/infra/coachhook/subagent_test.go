package coachhook

import (
	"os"
	"path/filepath"
	"testing"
)

// What a session's subagents spend is the session's spend. Their turns are
// in their own transcripts beside the session's and interleave with its
// turns in time, so each is counted once, however the Stops fall.
func TestEvaluate_CountsSubagentSpend(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()                                                                              // $50 budget
	tp := writeTranscript(t, dir, turnLine(ts(1), 20_000_000, opus), turnLine(ts(5), 20_000_000, opus)) // $20
	sub := filepath.Join(dir, "transcript", "subagents")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Written between the session's two turns: $10.
	rewrite(t, filepath.Join(sub, "agent-a1.jsonl"), turnLine(ts(2), 20_000_000, opus))

	d := Evaluate(dir, "s", tp, cfg, fixedNow)
	if !approx(d.CumulativeUSD, 30) {
		t.Fatalf("want $30 with the subagent's $10, got %.4f", d.CumulativeUSD)
	}
	if d = Evaluate(dir, "s", tp, cfg, fixedNow); !approx(d.CumulativeUSD, 30) {
		t.Fatalf("a repeated Stop counted again: %.4f", d.CumulativeUSD)
	}
	// The subagent goes on after the session's last turn: +$10.
	rewrite(t, filepath.Join(sub, "agent-a1.jsonl"), turnLine(ts(2), 20_000_000, opus), turnLine(ts(3), 20_000_000, opus))
	if d = Evaluate(dir, "s", tp, cfg, fixedNow); !approx(d.CumulativeUSD, 40) {
		t.Fatalf("want $40, got %.4f", d.CumulativeUSD)
	}
}
