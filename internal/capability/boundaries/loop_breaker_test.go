package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// toolCall is one PreToolUse call: the tool and its input's hash.
func toolCall(session, tool, hash string) Call {
	c := call(session)
	c.ToolName, c.ArgsHash = tool, hash
	return c
}

// run sends each "Tool:hash" call through the pause point and returns the
// verdicts.
func run(t *testing.T, r []domain.Rule, env Env, session string, tokens ...string) []Result {
	t.Helper()
	out := make([]Result, 0, len(tokens))
	for _, tok := range tokens {
		tool, hash, _ := strings.Cut(tok, ":")
		res, err := Pause(r, toolCall(session, tool, hash), env)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, res)
	}
	return out
}

func verdicts(rs []Result) string {
	vs := make([]string, len(rs))
	for i, r := range rs {
		vs[i] = r.Verdict
		if vs[i] == "" {
			vs[i] = "-"
		}
	}
	return strings.Join(vs, " ")
}

func TestLoopBreakerAtThePausePoint(t *testing.T) {
	enforced := domain.Promote(rules(t, config.LoopBreakerRule()), config.LoopBreakerName)
	cases := []struct {
		name     string
		attended bool
		calls    []string
		want     string
	}{
		{"a synthetic loop asks on its fourth call", true,
			[]string{"Bash:t", "Bash:t", "Bash:t", "Bash:t"}, "- - - ask"},
		{"unattended it refuses", false,
			[]string{"Bash:t", "Bash:t", "Bash:t", "Bash:t"}, "- - - deny"},
		{"a 2-cycle three times asks", true,
			[]string{"Bash:t", "Read:r", "Bash:t", "Read:r", "Bash:t", "Read:r"}, "- - - - - ask"},
		{"normal work stays quiet", true,
			[]string{"Read:a", "Grep:b", "Edit:e1", "Bash:t", "Read:a", "Edit:e2", "Bash:t", "Edit:e3", "Bash:t", "Read:c"},
			"- - - - - - - - - -"},
		{"an edit resets the window", true,
			[]string{"Bash:t", "Bash:t", "Bash:t", "Edit:e1", "Bash:t", "Bash:t", "Bash:t"}, "- - - - - - -"},
		{"the same failing edit is no progress", true,
			[]string{"Edit:e1", "Bash:t", "Edit:e1", "Bash:t", "Edit:e1", "Bash:t"}, "- - - - - ask"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t, tc.attended)
			got := run(t, enforced, env, "s1", tc.calls...)
			if v := verdicts(got); v != tc.want {
				t.Fatalf("verdicts = %s; want %s", v, tc.want)
			}
			last := got[len(got)-1]
			if last.Verdict == VerdictNone {
				return
			}
			for _, want := range []string{`"loop-breaker"`, "in a row with no file changed", "change approach", "ask the user"} {
				if !strings.Contains(last.Reason, want) {
					t.Errorf("reason %q lacks %q", last.Reason, want)
				}
			}
		})
	}
}

// In shadow the loop breaker records the session it would have paused,
// once, with its evidence, and lets every call through.
func TestLoopBreakerInShadowRecordsAndNeverBlocks(t *testing.T) {
	for _, attended := range []bool{false, true} {
		env := testEnv(t, attended)
		r := rules(t, config.LoopBreakerRule())
		got := run(t, r, env, "s1", "Bash:t", "Bash:t", "Bash:t", "Bash:t", "Bash:t", "Bash:t")
		if v := verdicts(got); v != "- - - - - -" {
			t.Fatalf("attended=%v: shadow verdicts = %s, want none", attended, v)
		}
		envs, err := env.Dir.Decision(domain.DecisionID(config.LoopBreakerName, "s1"))
		if err != nil {
			t.Fatal(err)
		}
		if len(envs) != 1 {
			t.Fatalf("attended=%v: %d decisions recorded, want 1", attended, len(envs))
		}
		d := envs[0].Payload.(*eventschema.DecisionEvent)
		if d.Stage != eventschema.DecisionStageShadow || !strings.Contains(d.Rationale, "the same Bash call 4 times in a row") {
			t.Fatalf("decision = %+v", d)
		}
		var loopEvidence bool
		for _, e := range d.Evidence {
			loopEvidence = loopEvidence || e.Kind == "tool_loop"
		}
		if !loopEvidence {
			t.Errorf("no tool_loop evidence on %+v", d.Evidence)
		}
		sessions, err := env.Dir.Sessions()
		if err != nil {
			t.Fatal(err)
		}
		var matched int
		for _, s := range sessions {
			matched += s.Matches[config.LoopBreakerName]
		}
		if matched != 3 {
			t.Errorf("attended=%v: %d matches counted, want 3 (calls 4 to 6)", attended, matched)
		}
		if pending, _ := env.Dir.ListPending(); len(pending) != 0 {
			t.Errorf("a shadow rule left something waiting: %v", pending)
		}
	}
}

// A call that names no tool input (opencode's hook sends none) gives no
// loop figure: the rule cannot see a loop and matches nothing, and no call
// is kept.
func TestLoopBreakerWithoutTheInputSeesNothing(t *testing.T) {
	env := testEnv(t, true)
	r := domain.Promote(rules(t, config.LoopBreakerRule()), config.LoopBreakerName)
	for range 6 {
		c := call("s1")
		c.ToolName = "bash"
		res, err := Pause(r, c, env)
		if err != nil || res.Verdict != VerdictNone {
			t.Fatalf("res = %+v, %v", res, err)
		}
	}
	if calls, _ := env.Dir.RecentCalls("s1"); len(calls) != 0 {
		t.Fatalf("kept %d calls without a hash", len(calls))
	}
}

// Without a loop rule the pause point keeps no calls at all.
func TestNoLoopRuleKeepsNoCalls(t *testing.T) {
	env := testEnv(t, true)
	run(t, rules(t, spendRule("ask", "enforce")), env, "s1", "Bash:t", "Bash:t", "Bash:t", "Bash:t")
	matches, _ := filepath.Glob(filepath.Join(env.Dir.Root(), "calls", "*"))
	if len(matches) != 0 {
		t.Fatalf("calls kept without a loop rule: %v", matches)
	}
}

// Only Claude Code's hook sends the tool's input, so status says the loop
// breaker is not evaluated anywhere else rather than claim it is.
func TestStatusSaysWhereTheLoopBreakerIsEvaluated(t *testing.T) {
	env := testEnv(t, false)
	st, err := RuleStatuses(rules(t, config.LoopBreakerRule()), env,
		map[string]bool{HarnessClaudeCode: true, HarnessOpencode: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{HarnessClaudeCode: EnforcementShadow, HarnessOpencode: EnforcementNotEvaluated,
		HarnessCodex: EnforcementNotEvaluated, HarnessCursor: EnforcementNotEvaluated}
	for _, h := range st.Rules[0].Harnesses {
		if want[h.Harness] != h.Enforcement {
			t.Errorf("%s = %q (%s); want %q", h.Harness, h.Enforcement, h.Note, want[h.Harness])
		}
	}
}

func TestLoopBreakerSummary(t *testing.T) {
	installed := map[string]bool{HarnessClaudeCode: true}
	if got, err := LoopBreaker(config.Config{}, testEnv(t, false), installed); err != nil || got.Configured {
		t.Fatalf("no rule: %+v, %v", got, err)
	}
	env := testEnv(t, false)
	cfg := config.Config{Boundaries: []domain.RuleSpec{config.LoopBreakerRule()}}
	r := rules(t, config.LoopBreakerRule())
	run(t, r, env, "s1", "Bash:t", "Bash:t", "Bash:t", "Bash:t")
	run(t, r, env, "s2", "Read:a", "Bash:t", "Read:a", "Bash:t", "Read:a", "Bash:t")
	run(t, r, env, "s3", "Read:a", "Edit:e", "Bash:t")
	old := env
	old.Now = func() time.Time { return testNow.Add(-8 * 24 * time.Hour) }
	run(t, r, old, "s-old", "Bash:t", "Bash:t", "Bash:t", "Bash:t")
	got, err := LoopBreaker(cfg, env, installed)
	if err != nil {
		t.Fatal(err)
	}
	want := PauseSummary{Configured: true, Rule: config.LoopBreakerName, Stage: "shadow", Sessions: 2, PausePoint: true,
		When: "the agent's calls go round a loop for 4 calls or more with no file changed", Require: "ask"}
	if got != want {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}
	// opencode's hook sends no input, so it is no pause point for loops.
	if got, _ := LoopBreaker(cfg, env, map[string]bool{HarnessOpencode: true}); got.PausePoint {
		t.Fatalf("opencode counted as a pause point: %+v", got)
	}
}

func TestAddLoopBreaker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("listen: 127.0.0.1:7878\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := AddLoopBreaker(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Added || got.Rule != config.LoopBreakerName || got.Stage != "shadow" {
		t.Fatalf("added = %+v", got)
	}
	before, _ := os.ReadFile(path)
	again, err := AddLoopBreaker(path)
	if err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); again.Added || string(before) != string(after) {
		t.Fatalf("a second add changed the config: %+v\n%s", again, after)
	}
	cfg, err := config.Load(path)
	if err != nil || !cfg.HasLoopBreaker() || cfg.BoundaryRules()[0].Enforced() {
		t.Fatalf("config = %+v, %v", cfg.Boundaries, err)
	}
}
