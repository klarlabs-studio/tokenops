package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/modellever"
	"go.klarlabs.de/tokenops/internal/infra/routeguard"
)

var allFour = []string{"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5", "claude-fable-5"}

// defaultModelFixture is a fake ~/.claude/settings.json in a temp home and
// an environment pressed like the maintainer's week.
func defaultModelFixture(t *testing.T, settings string) (coachcap.DefaultModelEnv, func() map[string]any) {
	t.Helper()
	home := t.TempDir()
	p := modellever.Paths{
		ClaudeSettings: filepath.Join(home, ".claude", "settings.json"),
		State:          filepath.Join(home, ".tokenops", "coach", "default-model.json"),
	}
	if err := os.MkdirAll(filepath.Dir(p.ClaudeSettings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ClaudeSettings, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	e := coachcap.DefaultModelEnv{
		Paths: p, Rung: config.AutonomyAutonomous,
		Pressure: &routeguard.Pressure{
			Vendor: "Claude", Window: "weekly", LeftPct: 54, RunsOutBeforeReset: 4*24*time.Hour + 6*time.Hour,
			ResetsAt: now.Add(5 * 24 * time.Hour), ObservedAt: now, Source: "claude_code_statusline",
		},
		Known: true, Session: "s1", SessionModel: "claude-opus-5",
		Catalog: routeCatalog(config.Config{}), Candidates: allFour, Ledger: coachLedger(), Now: now,
	}
	read := func() map[string]any {
		b, err := os.ReadFile(p.ClaudeSettings)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]any{}
		_ = json.Unmarshal(b, &m)
		return m
	}
	return e, read
}

// Autonomous under pressure sets new sessions one tier down, records the
// move with its evidence, and tells the operator how to switch this one.
func TestDefaultModelMovesDownUnderPressure(t *testing.T) {
	e, settings := defaultModelFixture(t, `{"env":{"X":"1"}}`)
	start := ledgerLen(t)
	msg := coachcap.ReconcileDefaultModel(e)
	if m := settings(); m["model"] != "sonnet" || m["env"] == nil {
		t.Fatalf("settings: %v", m)
	}
	if !strings.Contains(msg, "/model sonnet") || !strings.Contains(msg, "Claude weekly runs out") {
		t.Errorf("message: %q", msg)
	}
	l := ledgerSince(t, start)
	if len(l) != 1 || l[0]["channel"] != "move" || l[0]["kind"] != coachcap.DefaultModelKind || l[0]["to"] != "claude-sonnet-5" ||
		!strings.Contains(l[0]["evidence"].(string), "plan_pressure") {
		t.Fatalf("ledger gained %v", l)
	}
	// Idempotent: the next turn changes and says nothing.
	if msg := coachcap.ReconcileDefaultModel(e); msg != "" || ledgerLen(t) != start+1 {
		t.Errorf("second turn: %q, ledger %d", msg, ledgerLen(t)-start)
	}
}

// The default comes back once a fresh reading finds the plan no longer
// short, once the pressed window has reset, or once the power is lowered;
// no reading at all keeps it, so it does not flap.
func TestDefaultModelRevertsWhenTheReasonPasses(t *testing.T) {
	for name, clear := range map[string]func(*coachcap.DefaultModelEnv){
		"back on pace":  func(e *coachcap.DefaultModelEnv) { e.Pressure = nil },
		"window reset":  func(e *coachcap.DefaultModelEnv) { e.Pressure, e.Known, e.Now = nil, false, e.Now.Add(6*24*time.Hour) },
		"power lowered": func(e *coachcap.DefaultModelEnv) { e.Rung = config.AutonomyAdvise },
	} {
		t.Run(name, func(t *testing.T) {
			e, settings := defaultModelFixture(t, `{}`)
			coachcap.ReconcileDefaultModel(e)
			clear(&e)
			if msg := coachcap.ReconcileDefaultModel(e); !strings.Contains(msg, "own default model again") {
				t.Errorf("message: %q", msg)
			}
			if m := settings(); m["model"] != nil {
				t.Errorf("settings: %v", m)
			}
		})
	}
	e, settings := defaultModelFixture(t, `{}`)
	coachcap.ReconcileDefaultModel(e)
	e.Pressure, e.Known = nil, false
	if msg := coachcap.ReconcileDefaultModel(e); msg != "" || settings()["model"] != "sonnet" {
		t.Errorf("no reading reverted: %q %v", msg, settings())
	}
}

// A model the operator set is never overridden: the coach advises /model
// instead, once per pressed window.
func TestDefaultModelAdvisesOverTheOperatorsModel(t *testing.T) {
	e, settings := defaultModelFixture(t, `{"model":"opus"}`)
	msg := coachcap.ReconcileDefaultModel(e)
	if settings()["model"] != "opus" {
		t.Fatalf("operator's model overridden: %v", settings())
	}
	if !strings.Contains(msg, "/model sonnet") {
		t.Errorf("no advice: %q", msg)
	}
	if again := coachcap.ReconcileDefaultModel(e); again != "" {
		t.Errorf("advised twice in one window: %q", again)
	}
}

// ask and advise only advise; off does nothing; quiet says nothing.
func TestDefaultModelBelowAutonomous(t *testing.T) {
	for _, rung := range []string{config.AutonomyAsk, config.AutonomyAdvise} {
		e, settings := defaultModelFixture(t, `{}`)
		e.Rung = rung
		if msg := coachcap.ReconcileDefaultModel(e); !strings.Contains(msg, "/model sonnet") || settings()["model"] != nil {
			t.Errorf("%s: %q %v", rung, msg, settings())
		}
	}
	e, settings := defaultModelFixture(t, `{}`)
	e.Rung = config.AutonomyOff
	if msg := coachcap.ReconcileDefaultModel(e); msg != "" || settings()["model"] != nil {
		t.Errorf("off: %q %v", msg, settings())
	}
	e, settings = defaultModelFixture(t, `{}`)
	e.Verbosity = config.VerbosityQuiet
	if msg := coachcap.ReconcileDefaultModel(e); msg != "" || settings()["model"] != "sonnet" {
		t.Errorf("quiet: %q %v", msg, settings())
	}
}

// The model policy wins: with sonnet ruled out nothing is set, rather than
// a model two tiers down or a forbidden one.
func TestDefaultModelRespectsThePolicy(t *testing.T) {
	e, settings := defaultModelFixture(t, `{}`)
	e.Candidates = config.Config{ModelPolicy: config.ModelPolicyConfig{Deny: []string{"claude-sonnet-*"}}}.ModelPolicy.Policy().Filter("anthropic", allFour)
	if msg := coachcap.ReconcileDefaultModel(e); msg != "" || settings()["model"] != nil {
		t.Errorf("policy: %q %v", msg, settings())
	}
}

// No pressure, no change.
func TestDefaultModelUnchangedWithoutPressure(t *testing.T) {
	e, settings := defaultModelFixture(t, `{}`)
	e.Pressure = nil
	if msg := coachcap.ReconcileDefaultModel(e); msg != "" || settings()["model"] != nil {
		t.Errorf("%q %v", msg, settings())
	}
}

// End to end through the Stop hook: a pressed week with models autonomous
// sets the default in the (sandboxed) ~/.claude/settings.json and says so;
// the operator's turn-end message carries it.
func TestCoachHookSetsTheDefaultModelUnderPressure(t *testing.T) {
	writeCoachConfig(t, fourTiers+"coach:\n  autonomy: advise\n  powers:\n    models: autonomous\n")
	writePressedWeek(t)
	paths, err := modellever.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.ClaudeSettings), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(paths.ClaudeSettings); _ = os.Remove(paths.State) })
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	out := runCoach(t, dir, tp, "dm-1")
	if !strings.Contains(out, "/model sonnet") {
		t.Fatalf("hook said %q", out)
	}
	b, err := os.ReadFile(paths.ClaudeSettings)
	if err != nil || !strings.Contains(string(b), `"model": "sonnet"`) {
		t.Fatalf("settings: %s (%v)", b, err)
	}
}

// A retracted default-model move (its sessions did worse, VerifyMoves) is
// not made: the coach advises /model instead. One already in place is
// reverted on the next turn.
func TestDefaultModelRetractedIsNotMadeAndReverts(t *testing.T) {
	retracted := func(kind, from, to string) bool {
		return kind == coachcap.DefaultModelKind && from == "opus" && to == "sonnet"
	}
	e, settings := defaultModelFixture(t, `{}`)
	e.Retracted = retracted
	msg := coachcap.ReconcileDefaultModel(e)
	if _, set := settings()["model"]; set {
		t.Fatalf("a retracted move was made: %v", settings())
	}
	if !strings.Contains(msg, "/model sonnet") {
		t.Errorf("no advice instead: %q", msg)
	}

	e, settings = defaultModelFixture(t, `{}`)
	if coachcap.ReconcileDefaultModel(e); settings()["model"] != "sonnet" {
		t.Fatalf("setup: lever not applied: %v", settings())
	}
	e.Retracted = retracted
	msg = coachcap.ReconcileDefaultModel(e)
	if _, set := settings()["model"]; set {
		t.Fatalf("retracted lever left in place: %v", settings())
	}
	if !strings.Contains(msg, "retracted") {
		t.Errorf("revert not explained: %q", msg)
	}
}

// The Stop hook records the session's subagent outcomes as evidence for
// judging the coach's moves, without a word to the operator.
func TestStopHookRecordsMoveEvidence(t *testing.T) {
	writeCoachConfig(t, "coach:\n  autonomy: advise\n")
	store := coachcap.MoveStore()
	// Other hook tests share the sandbox home and may have judged within
	// the last few minutes, which would hold this pass back.
	_ = os.RemoveAll(store.Dir)
	t.Cleanup(func() { _ = os.RemoveAll(store.Dir) })
	dir := t.TempDir()
	lines := strings.Join([]string{
		`{"type":"user","timestamp":"2026-07-07T12:00:00Z","sessionId":"verify1","message":{"role":"user","content":"tidy the parser"}}`,
		`{"type":"assistant","timestamp":"2026-07-07T12:00:01Z","sessionId":"verify1","message":{"model":"claude-opus-5","content":[{"type":"tool_use","id":"toolu_v","name":"Agent","input":{"description":"Add a test for the parser","prompt":"add it"}}]}}`,
		`{"type":"user","timestamp":"2026-07-07T12:02:00Z","sessionId":"verify1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_v","content":"done"}]}}`,
	}, "\n") + "\n"
	tp := filepath.Join(dir, "verify1.jsonl")
	if err := os.WriteFile(tp, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	runCoach(t, t.TempDir(), tp, "verify1")
	f := store.LoadSession("verify1")
	if len(f.Calls) != 1 || f.Calls[0].Ref != "toolu_v" || f.Calls[0].Model != "opus" || f.Calls[0].Kind != "edit" || f.Start == nil {
		t.Fatalf("evidence = %+v", f)
	}
}
