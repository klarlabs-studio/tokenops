package checkup

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var t0 = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

func tool(session, name, path string, min int) agentdx.Record {
	return agentdx.Record{SessionID: session, Kind: agentdx.KindToolUse, ToolName: name, FilePath: path, At: at(min)}
}

// A read of a file already read, with no write in between, is a re-read;
// a read after an edit is not, and neither is the first read in another
// session. With the guard on, the fix says so instead of installing it.
func TestRereads(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte(strings.Repeat("x", 4000)), 0o600); err != nil {
		t.Fatal(err)
	}
	records := []agentdx.Record{
		tool("a", "Read", file, 3), tool("a", "Read", file, 1), // out of order on purpose
		tool("a", "Edit", file, 4), tool("a", "Read", file, 5), // after an edit: fresh
		tool("a", "Read", file, 6), // re-read
		tool("b", "Read", file, 1), // other session: first read
	}
	f := rereads(records, false)
	if len(f) != 1 || !strings.Contains(f[0].Evidence, "2 re-reads of 1 files across 1 sessions") ||
		!strings.Contains(f[0].Evidence, "about 2k tokens") || !strings.Contains(f[0].Fix, "--read-guard") {
		t.Fatalf("rereads = %+v", f)
	}
	if on := rereads(records, true); on[0].Level != LevelInfo || strings.Contains(on[0].Fix, "install") {
		t.Errorf("guard on = %+v", on[0])
	}
	if rereads([]agentdx.Record{tool("a", "Read", file, 1)}, false) != nil {
		t.Error("a single read reported")
	}
}

// Instruction files count on every turn in their directory, the global
// one on every turn, and a parent's file on a child directory's turns.
func TestStandingContext(t *testing.T) {
	home := t.TempDir()
	write := func(path string, bytes int) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Repeat("x", bytes)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".claude", "CLAUDE.md"), 4000)        // 1k tokens
	write(filepath.Join(home, "repo", "AGENTS.md"), 8000)           // 2k tokens
	write(filepath.Join(home, "repo", "sub", "CLAUDE.md"), 4000)    // 1k tokens
	turns := map[string]int{filepath.Join(home, "repo", "sub"): 10} // 4k tokens a turn
	f := standingContext(home, turns, Usage{InputTokens: 400_000})
	if len(f) != 1 || !strings.Contains(f[0].Evidence, "40k tokens, 10.0% of all input") || f[0].Level != LevelNotice {
		t.Fatalf("standing = %+v", f)
	}
	if standingContext(home, nil, Usage{InputTokens: 1}) != nil {
		t.Error("no turns, yet a finding")
	}
}

// A lookup answered on a deep-tier model counts; other kinds do not, and
// fewer than ten lookups is too few to say anything.
func TestOversized(t *testing.T) {
	records := make([]agentdx.Record, 0, 24)
	for i := range 12 {
		s := string(rune('a' + i))
		records = append(records,
			agentdx.Record{SessionID: s, Kind: agentdx.KindPrompt, Text: "show me the config file", At: at(0)},
			agentdx.Record{SessionID: s, Kind: agentdx.KindAssistantTurn, Model: "claude-opus-4-1", At: at(1)},
		)
	}
	f := oversized(records, false)
	if len(f) != 1 || !strings.Contains(f[0].Title, "12 of 12 lookups") {
		t.Fatalf("oversized = %+v", f)
	}
	if oversized(records[:4], false) != nil {
		t.Error("two lookups reported")
	}
}

// Turns are priced per harness and model; a model the card does not know
// is counted and left unpriced, never guessed.
func TestTally(t *testing.T) {
	tl := &tally{engine: spend.NewEngine(spend.DefaultTable()), by: map[[2]string]*Usage{}}
	tl.add(turn{harness: "Claude Code", model: "claude-sonnet-5", provider: eventschema.ProviderAnthropic, at: t0,
		input: 1_000_000, cached: 900_000, output: 10_000})
	tl.add(turn{harness: "Codex", model: "no-such-model", provider: eventschema.ProviderOpenAI, at: t0, input: 5, output: 5})
	u := tl.by[[2]string{"Claude Code", "claude-sonnet-5"}]
	if u.Turns != 1 || u.CostUSD <= 0 || u.UnpricedTurns != 0 {
		t.Errorf("priced = %+v", u)
	}
	x := tl.by[[2]string{"Codex", "no-such-model"}]
	if x.UnpricedTurns != 1 || x.CostUSD != 0 {
		t.Errorf("unpriced = %+v", x)
	}
}

// A cache write is part of a turn's input but bills above it, so a
// write-heavy turn is worth more than the same tokens as plain input.
func TestTallyPricesCacheWrites(t *testing.T) {
	value := func(tn turn) float64 {
		tl := &tally{engine: spend.NewEngine(spend.DefaultTable()), by: map[[2]string]*Usage{}}
		tl.add(tn)
		return tl.by[[2]string{tn.harness, tn.model}].CostUSD
	}
	plain := turn{harness: "Claude Code", model: "claude-sonnet-5", provider: eventschema.ProviderAnthropic, at: t0, input: 1_000_000}
	written := plain
	written.written, written.written1h = 1_000_000, 500_000
	// claude-sonnet-5: $2 input, so writes are $2.50 (5m) and $4 (1h).
	if got, want := value(written)-value(plain), 0.5*2.5+0.5*4-2.0; math.Abs(got-want) > 1e-9 {
		t.Errorf("write premium = %v, want %v", got, want)
	}
}
