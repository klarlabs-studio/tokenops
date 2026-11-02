package boundaries

import (
	"slices"
	"strings"
	"testing"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
)

func TestPlansStartedByCommand(t *testing.T) {
	for _, tc := range []struct {
		command, harness string
		want             []string
	}{
		{`codex exec "fix the build"`, HarnessClaudeCode, []string{"openai"}},
		{`cd api && CODEX_HOME=/tmp/c /opt/homebrew/bin/codex exec -m gpt-6.1 'review'`, HarnessClaudeCode, []string{"openai"}},
		{`npx @openai/codex exec x`, HarnessClaudeCode, []string{"openai"}},
		{`timeout 600 gemini -p "summarise"`, HarnessClaudeCode, []string{"gemini"}},
		{`cursor-agent -p hi | tee out.txt`, HarnessClaudeCode, []string{"cursor"}},
		{`copilot -p "write tests"; codex exec more`, HarnessClaudeCode, []string{"github", "openai"}},
		{`claude -p "plan this"`, HarnessOpencode, []string{"anthropic"}},
		// Claude Code starting Claude Code spends its own plan, already read.
		{`claude -p "plan this"`, HarnessClaudeCode, nil},
		// Names that only mention an agent do not start one.
		{`git log --grep codex`, HarnessClaudeCode, nil},
		{`cat codex.md && ls ~/.codex`, HarnessClaudeCode, nil},
		{`echo "run codex exec later"`, HarnessClaudeCode, nil},
		{`brew upgrade codex`, HarnessClaudeCode, nil},
		{``, HarnessClaudeCode, nil},
	} {
		got := PlansStartedBy(tc.command, tc.harness)
		if !slices.Equal(got, tc.want) {
			t.Errorf("PlansStartedBy(%q, %s) = %v, want %v", tc.command, tc.harness, got, tc.want)
		}
	}
}

// A tool call that starts another agent spends that agent's plan, so a
// headroom rule reads it beside the session's own: Claude Code with room
// starting Codex with none is paused, and the reason says whose plan.
func TestACrossAgentCallReadsThePlanItSpends(t *testing.T) {
	env := testEnv(t, false)
	r := rules(t, domain.RuleSpec{Name: "headroom-pause", Action: "continue", Require: "deny", Stage: "enforce",
		When: map[string]string{"plan_left_pct": "<= 10"}})
	writeLimits(t, env, 40) // Claude: 60% left of the week
	putPlanLeft(t, env, map[string]map[string]float64{"openai": {"5h": 70, "week": 6}, "anthropic": {"week": 60}})

	plain := call("s1")
	if res, _ := Guard(r, plain, env); res.Verdict != VerdictNone {
		t.Fatalf("a call that starts no agent read another plan: %+v", res)
	}
	cross := call("s2")
	cross.Spends = []string{"openai"}
	res, _ := Guard(r, cross, env)
	if res.Verdict != VerdictDeny || !strings.Contains(res.Reason, "Codex plan left 6.0% <= 10% (week)") {
		t.Fatalf("a Codex call with Codex used up = %+v", res)
	}

	// The session's own plan still binds when it is the tighter one.
	writeLimits(t, env, 95)
	putPlanLeft(t, env, map[string]map[string]float64{"openai": {"week": 80}})
	cross.SessionID = "s3"
	res, _ = Guard(r, cross, env)
	if res.Verdict != VerdictDeny || !strings.Contains(res.Reason, "Claude plan left 5.0%") {
		t.Fatalf("Claude used up, Codex roomy = %+v", res)
	}

	// No reading of the spent plan says nothing about it.
	writeLimits(t, env, 40)
	putPlanLeft(t, env, map[string]map[string]float64{"anthropic": {"week": 60}})
	cross.SessionID = "s4"
	if res, _ := Guard(r, cross, env); res.Verdict != VerdictNone {
		t.Fatalf("an unread Codex plan paused: %+v", res)
	}
}

func putPlanLeft(t *testing.T, env Env, left map[string]map[string]float64) {
	t.Helper()
	if err := env.Dir.PutFigures(boundaryledger.Figures{ObservedAt: testNow, PlanLeftPct: left}); err != nil {
		t.Fatal(err)
	}
}
