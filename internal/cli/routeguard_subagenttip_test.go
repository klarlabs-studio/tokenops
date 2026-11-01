package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// subagentNote runs the Agent hook and returns its note, failing when the
// output carries anything that could change the call or its prompt.
func subagentNote(t *testing.T, dir string, input map[string]any, tp string) string {
	t.Helper()
	out := runSubagentHook(t, dir, input, tp)
	if out == "" {
		return ""
	}
	if strings.Contains(out, "permissionDecision") || strings.Contains(out, "updatedInput") {
		t.Fatalf("a note-only output carries a decision: %s", out)
	}
	var h hookOut
	if err := json.Unmarshal([]byte(out), &h); err != nil {
		t.Fatalf("hook output is not JSON: %q", out)
	}
	return h.HookSpecificOutput.AdditionalContext
}

var lookupLaunch = map[string]any{
	"subagent_type": "Explore", "description": "find config", "prompt": "find where the retention config is defined",
}

// At models: advise, a subagent on a model above what its work needs gets
// a note naming its type and the model to pass, and only when the coach
// may speak.
func TestSubagentTipAtAdvise(t *testing.T) {
	cases := []struct {
		name  string
		coach string
		input map[string]any
		want  string
	}{
		{"advise names the type and the model", "coach:\n  autonomy: advise\n", lookupLaunch, `this Explore subagent runs on claude-opus-5; its work looks like lookup, which claude-haiku-4-5 fits. Pass model: "haiku"`},
		{"a launch that fits says nothing", "coach:\n  autonomy: advise\n", map[string]any{"subagent_type": "Explore", "description": "find config", "model": "haiku"}, ""},
		{"quiet says nothing", "coach:\n  autonomy: advise\n  verbosity: quiet\n", lookupLaunch, ""},
		{"inform off says nothing", "coach:\n  autonomy: advise\n  powers:\n    inform: off\n", lookupLaunch, ""},
		{"models off says nothing", "coach:\n  autonomy: advise\n  powers:\n    models: off\n", lookupLaunch, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateHome(t)
			writeCoachConfig(t, models+c.coach)
			dir := t.TempDir()
			tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
			got := subagentNote(t, dir, c.input, tp)
			if c.want == "" && got != "" {
				t.Fatalf("note = %q; want none", got)
			}
			if c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("note = %q; want it to contain %q", got, c.want)
			}
		})
	}
}

// The next launch of the same type settles the tip: the same model again
// is ignoring it, recorded with what was launched instead and seen by the
// agent; and the tip is not repeated within the cool-down.
func TestSubagentTipIsSettledByTheNextLaunch(t *testing.T) {
	isolateHome(t)
	writeCoachConfig(t, models+"coach:\n  autonomy: advise\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	if subagentNote(t, dir, lookupLaunch, tp) == "" {
		t.Fatal("no tip at the first launch")
	}
	if note := subagentNote(t, dir, lookupLaunch, tp); note != "" {
		t.Fatalf("the tip was repeated within the cool-down: %q", note)
	}
	entries, err := coachLedger().Load()
	if err != nil {
		t.Fatal(err)
	}
	var offer, seen, resolve bool
	for _, e := range entries {
		switch e.Type {
		case "offer":
			offer = offer || (e.Kind == "subagent_model" && e.Point == "subagent_launch" && e.Object == "Explore" && e.Via == "agent")
		case "seen":
			seen = e.Via == "agent"
		case "resolve":
			resolve = resolve || (e.Kind == "subagent_model" && e.Outcome == "ignored" && e.Instead == "launched Explore on opus again")
		}
	}
	if !offer || !seen || !resolve {
		t.Fatalf("ledger = %+v", entries)
	}
}

// A cheaper launch of the same type follows the tip.
func TestSubagentTipFollowedByACheaperLaunch(t *testing.T) {
	isolateHome(t)
	writeCoachConfig(t, models+"coach:\n  autonomy: advise\n")
	dir := t.TempDir()
	tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
	if subagentNote(t, dir, lookupLaunch, tp) == "" {
		t.Fatal("no tip at the first launch")
	}
	cheaper := map[string]any{"subagent_type": "Explore", "description": "find config", "model": "haiku"}
	subagentNote(t, dir, cheaper, tp)
	entries, _ := coachLedger().Load()
	followed := false
	for _, e := range entries {
		if e.Type == "resolve" && e.Kind == "subagent_model" && e.Outcome == "followed" && e.Instead == "" {
			followed = true
		}
	}
	if !followed {
		t.Fatalf("ledger = %+v; want the tip followed", entries)
	}
}

// The per-turn model advice is a tip at the turn's model choice: it is
// recorded with the model it names, and the throttle holds the same
// advice within the cool-down even where the route guard's own state
// would argue it again. Verbose still advises.
func TestRouteAdviceIsADecisionPointTip(t *testing.T) {
	for _, tc := range []struct {
		name      string
		verbosity string
		again     bool
	}{
		{"normal holds a repeat", "", false},
		{"verbose still advises", "  verbosity: verbose\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			writeCoachConfig(t, models+"coach:\n  autonomy: advise\n"+tc.verbosity)
			dir := t.TempDir()
			tp := writeCoachTranscript(t, dir, 1, "claude-opus-5")
			if out := runPromptHook(t, dir, "show me the retention config", tp); out == "" {
				t.Fatal("no advice given")
			}
			entries, _ := coachLedger().Load()
			if len(entries) == 0 || entries[0].Point != "model_choice" || entries[0].Object != "claude-haiku-4-5" {
				t.Fatalf("ledger = %+v; want the advice at the model choice naming the model", entries)
			}
			// A fresh route-guard state would argue the case again.
			again := runPromptHook(t, t.TempDir(), "show me the retention config", tp) != ""
			if again != tc.again {
				t.Fatalf("advised again = %v; want %v", again, tc.again)
			}
		})
	}
}
