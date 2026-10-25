package coach

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	mv "go.klarlabs.de/tokenops/internal/contexts/coaching/moveverdict"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/modellever"
	"go.klarlabs.de/tokenops/internal/infra/moveverify"
	"go.klarlabs.de/tokenops/internal/infra/routeguard"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

var verifyT0 = time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)

func line(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ts(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }

// subagentCall is one Agent call and its result, an hour apart from the
// next so none reads as a re-run of another.
type subagentCall struct {
	id, model string
	failed    bool
}

func writeSubagentSession(t *testing.T, dir, session string, calls []subagentCall) string {
	t.Helper()
	lines := make([]string, 0, 1+2*len(calls))
	lines = append(lines, line(t, map[string]any{"type": "user", "timestamp": ts(verifyT0), "sessionId": session,
		"message": map[string]any{"role": "user", "content": "work through the parser backlog"}}))
	for i, c := range calls {
		at := verifyT0.Add(time.Duration(i+1) * time.Hour)
		input := map[string]any{"description": "Add a test for the parser", "prompt": "Add a test to internal/parse/parser_test.go"}
		if c.model != "" {
			input["model"] = c.model
		}
		lines = append(lines, line(t, map[string]any{"type": "assistant", "timestamp": ts(at), "sessionId": session,
			"message": map[string]any{"role": "assistant", "model": "claude-opus-5", "content": []any{
				map[string]any{"type": "tool_use", "id": c.id, "name": "Agent", "input": input}}}}))
		lines = append(lines, line(t, map[string]any{"type": "user", "timestamp": ts(at.Add(2 * time.Minute)), "sessionId": session,
			"message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": c.id, "is_error": c.failed, "content": "done"}}}}))
	}
	return writeTranscript(t, dir, session, lines)
}

func writeTranscript(t *testing.T, dir, session string, lines []string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, session+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Subagents the coach moved from opus to sonnet for edit work fail far
// more often than edit subagents left on opus in the same project. The
// Stop hook's verification retracts the move once, says so once, and the
// route guard stops making it.
func TestVerifyMovesRetractsASubagentMoveThatHurtQuality(t *testing.T) {
	project := filepath.Join(t.TempDir(), "-repo-p")
	var calls []subagentCall
	l := &memLedger{}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("mv%d", i)
		calls = append(calls, subagentCall{id: id, model: "sonnet", failed: i < 6})
		RecordSubagentMove(l, verifyT0, "move-"+id, "s1", id, "edit", "claude-opus-5", "claude-sonnet-5", "")
	}
	for i := 0; i < 10; i++ {
		calls = append(calls, subagentCall{id: fmt.Sprintf("base%d", i), model: "opus", failed: i < 1})
	}
	transcript := writeSubagentSession(t, project, "s1", calls)
	store := moveverify.Store{Dir: t.TempDir()}
	e := VerifyEnv{Store: store, Session: "s1", Transcript: transcript, Ledger: l, Now: verifyT0.Add(30 * time.Hour)}

	msg := VerifyMoves(e)
	if !strings.Contains(msg, "stopped moving edit subagents from opus to sonnet") || !strings.Contains(msg, "tokenops coach reinstate edit") {
		t.Fatalf("message = %q", msg)
	}
	st := store.Load()
	if len(st.Retractions) != 1 {
		t.Fatalf("retractions = %+v", st.Retractions)
	}
	r := st.Retractions[0]
	if r.Key != (mv.Key{Kind: "edit", From: "opus", To: "sonnet"}) || r.Evidence.Judged != 10 || r.Evidence.Baseline != 10 || len(r.Alternatives) == 0 {
		t.Fatalf("retraction = %+v", r)
	}
	// Surfaced once: judged again later, nothing more is said.
	e.Now = e.Now.Add(time.Hour)
	if again := VerifyMoves(e); again != "" {
		t.Fatalf("told twice: %q", again)
	}
	// The route guard no longer makes the move, even under pressure.
	in := routeguard.SubagentInput{
		Description: "Add a test for the parser", Requested: "opus", Provider: eventschema.ProviderAnthropic,
		Catalog: routeguard.NewCatalog(spend.Table{Currency: "USD", Rates: map[spend.Key]spend.Rate{
			{Provider: "anthropic", Model: "claude-haiku-4-5"}: {InputPerMillion: 1, OutputPerMillion: 5},
			{Provider: "anthropic", Model: "claude-sonnet-5"}:  {InputPerMillion: 2, OutputPerMillion: 10},
			{Provider: "anthropic", Model: "claude-opus-5"}:    {InputPerMillion: 5, OutputPerMillion: 25},
		}}),
		Candidates: []string{"claude-haiku-4-5", "claude-sonnet-5", "claude-opus-5"},
		Pressure:   &routeguard.Pressure{Vendor: "Claude", Window: "weekly", LeftPct: 20},
		Retracted:  store.Retracted(),
	}
	if d := routeguard.EvaluateSubagent(in); d.Rewrite || d.Held == "" {
		t.Fatalf("retracted move still made: %+v", d)
	}
	// The explanation names the move kind, the counts and the verdict.
	v := Verdicts(store, e.Now)
	text := strings.Join(v.Lines, "\n")
	for _, want := range []string{"edit work, opus → sonnet", "10 moves", "60% had a bad outcome vs 10%", "retracted"} {
		if !strings.Contains(text, want) {
			t.Errorf("verdict lines lack %q:\n%s", want, text)
		}
	}
	// The operator can reinstate it.
	ended, err := Reinstate(store, "edit", e.Now)
	if err != nil || len(ended) != 1 || store.Retracted()("edit", "opus", "sonnet") {
		t.Fatalf("reinstate: %+v, %v", ended, err)
	}
}

// sessionUnits writes a session of five instructions on model; rejects
// marks how many of the first four the next instruction rejects.
func writeUnitSession(t *testing.T, dir, session, model string, start time.Time, rejects int) string {
	t.Helper()
	var lines []string
	for i := 0; i < 5; i++ {
		at := start.Add(time.Duration(i) * 10 * time.Minute)
		text := fmt.Sprintf("task number %d", i)
		if i > 0 && i <= rejects {
			text = "no, that's wrong"
		}
		lines = append(lines,
			line(t, map[string]any{"type": "user", "timestamp": ts(at), "sessionId": session, "message": map[string]any{"role": "user", "content": text}}),
			line(t, map[string]any{"type": "assistant", "timestamp": ts(at.Add(time.Minute)), "sessionId": session,
				"message": map[string]any{"role": "assistant", "model": model, "content": []any{map[string]any{"type": "text", "text": "ok"}}}}))
	}
	return writeTranscript(t, dir, session, lines)
}

// Sessions started on the coach's lowered default get rejected far more
// often than sessions on the operator's own model. Verification retracts
// the default-model move, reverts the lever at once and records the move
// as undone, with why.
func TestVerifyMovesRevertsTheDefaultModelWhenSessionsDoWorse(t *testing.T) {
	home := t.TempDir()
	paths := modellever.Paths{
		ClaudeSettings: filepath.Join(home, ".claude", "settings.json"),
		State:          filepath.Join(home, ".tokenops", "coach", "default-model.json"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.ClaudeSettings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ClaudeSettings, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err := modellever.Apply(paths, modellever.Setting{Value: "sonnet", From: "claude-opus-5", MoveID: "lv1"}, verifyT0); err != nil || r.Status != modellever.StatusApplied {
		t.Fatalf("apply: %+v %v", r, err)
	}
	l := &memLedger{}
	RecordMoveBecause(l, verifyT0, "lv1", "s0", config.PowerModels, DefaultModelKind, "claude-opus-5", "claude-sonnet-5", "")
	project := filepath.Join(t.TempDir(), "-repo-p")
	store := moveverify.Store{Dir: t.TempDir()}
	now := verifyT0.Add(time.Hour)
	var msg string
	for i := 0; i < 4; i++ {
		for _, s := range []struct {
			id, model string
			rejects   int
		}{
			{fmt.Sprintf("low%d", i), "claude-sonnet-5", 4},
			{fmt.Sprintf("own%d", i), "claude-opus-5", 0},
		} {
			start := verifyT0.Add(time.Duration(i+1) * 2 * time.Hour)
			tr := writeUnitSession(t, project, s.id, s.model, start, s.rejects)
			now = now.Add(judgeEvery + time.Minute)
			msg = joinLines(msg, VerifyMoves(VerifyEnv{Store: store, Session: s.id, Transcript: tr, Ledger: l, Lever: paths, Now: now}))
		}
	}
	if !strings.Contains(msg, "stopped starting new sessions on sonnet instead of opus") || !strings.Contains(msg, "own default model again") {
		t.Fatalf("message = %q", msg)
	}
	b, err := os.ReadFile(paths.ClaudeSettings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "model") {
		t.Fatalf("lever not reverted: %s", b)
	}
	var undone *ft.Record
	for _, rec := range ft.Fold(l.entries, now) {
		if rec.Offer.ID == "lv1" {
			undone = &rec
		}
	}
	if undone == nil || undone.Outcome != ft.OutcomeUndone || !strings.Contains(undone.Evidence, "retracted") {
		t.Fatalf("lever move record = %+v", undone)
	}
}

func joinLines(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "\n" + b
}

func TestVerifyMovesFailsOpen(t *testing.T) {
	for name, e := range map[string]VerifyEnv{
		"no store":      {Session: "s", Transcript: "x"},
		"no session":    {Store: moveverify.Store{Dir: t.TempDir()}, Transcript: "x"},
		"no transcript": {Store: moveverify.Store{Dir: t.TempDir()}, Session: "s"},
		"missing file":  {Store: moveverify.Store{Dir: t.TempDir()}, Session: "s", Transcript: filepath.Join(t.TempDir(), "none.jsonl"), Now: verifyT0},
	} {
		if msg := VerifyMoves(e); msg != "" {
			t.Errorf("%s: %q", name, msg)
		}
	}
}
