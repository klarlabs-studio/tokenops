package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// boundaryHome gives one test its own home: the config, the coach hook's
// session state and the boundary ledger all live under it.
func boundaryHome(t *testing.T, cfg string, sessionCost float64) string {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, home)
	}
	writeCoachConfig(t, cfg)
	coach := filepath.Join(home, ".tokenops", "coach-hook")
	if err := os.MkdirAll(coach, 0o700); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"cumulative_usd": sessionCost})
	if err := os.WriteFile(filepath.Join(coach, "session-s1.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func runCLI(t *testing.T, stdin []byte, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs(args)
	root.SetIn(bytes.NewReader(stdin))
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	return strings.TrimSpace(out.String()), err
}

func runBoundaryHook(t *testing.T, args ...string) hookDecision {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"session_id": "s1", "transcript_path": "/x/.claude/projects/-repo/s1.jsonl", "tool_use_id": "tu-1",
		"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "secret prompt-like text"},
	})
	out, err := runCLI(t, body, append([]string{"boundary-guard"}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	var d hookDecision
	if out != "" {
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatalf("hook output %q: %v", out, err)
		}
	}
	return d
}

const spendBoundary = `boundaries:
  - name: session-spend
    action: continue
    when: { session_cost_usd: ">= 20" }
    require: ask
`

func ledger(t *testing.T, home string) []*eventschema.Envelope {
	t.Helper()
	envs, err := boundaryledger.Dir(filepath.Join(home, ".tokenops", "boundaries")).Read()
	if err != nil {
		t.Fatal(err)
	}
	return envs
}

func TestBoundaryHookShadowChangesNothing(t *testing.T) {
	home := boundaryHome(t, spendBoundary, 25)
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("a shadow rule changed the call: %+v", d)
	}
	envs := ledger(t, home)
	if len(envs) != 1 {
		t.Fatalf("ledger = %d entries; want the shadow decision", len(envs))
	}
	p := envs[0].Payload.(*eventschema.DecisionEvent)
	if p.Stage != eventschema.DecisionStageShadow || p.Policy != "boundary:session-spend" || p.Actor.AgentID != "claude-code:-repo" {
		t.Fatalf("decision = %+v", p)
	}
	b, _ := json.Marshal(envs[0])
	if strings.Contains(string(b), "secret prompt-like text") {
		t.Fatal("the tool's input reached the decision record")
	}
}

func TestBoundaryHookUnattendedRefusesUntilApproved(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "0")
	home := boundaryHome(t, spendBoundary+"    stage: enforce\n", 25)
	d := runBoundaryHook(t)
	h := d.HookSpecificOutput
	if h.PermissionDecision != "deny" || !strings.Contains(h.PermissionDecisionReason, "tokenops approve bnd-") {
		t.Fatalf("decision = %+v", h)
	}
	id := strings.Fields(h.PermissionDecisionReason[strings.Index(h.PermissionDecisionReason, "tokenops approve ")+len("tokenops approve "):])[0]
	id = strings.Trim(id, "`,.")

	status, err := runCLI(t, nil, "boundaries", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, id) || !strings.Contains(status, `"stage": "enforce"`) {
		t.Fatalf("status does not list the waiting decision:\n%s", status)
	}
	// An agent's shell has no terminal: its approval is refused.
	if _, err := runCLI(t, nil, "approve", id, "--session"); err == nil || !strings.Contains(err.Error(), "needs a terminal") {
		t.Fatalf("approve without a terminal = %v, want refused", err)
	}
	if out, err := runCLI(t, nil, "deny", id); err != nil || !strings.Contains(out, "denied") {
		t.Fatalf("deny without a terminal = %q, %v; denying needs no person", out, err)
	}
	atTerminal(t)
	if out, err := runCLI(t, nil, "approve", id, "--session"); err != nil || !strings.Contains(out, "approved") {
		t.Fatalf("approve = %q, %v", out, err)
	}
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("after approval: %+v", d)
	}
	var accepted bool
	for _, e := range ledger(t, home) {
		p := e.Payload.(*eventschema.DecisionEvent)
		if p.Stage == eventschema.DecisionStageAccepted && p.Approval.Method == "cli" && p.Approval.Scope == "session" {
			accepted = true
		}
	}
	if !accepted {
		t.Fatal("the approval is not on the decision")
	}
	explained, err := runCLI(t, nil, "explain", id)
	if err != nil || !strings.Contains(explained, `"rule": "session-spend"`) || !strings.Contains(explained, `"approver"`) {
		t.Fatalf("explain = %s, %v", explained, err)
	}
}

func TestBoundaryHookAttendedAsks(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "1")
	boundaryHome(t, spendBoundary+"    stage: enforce\n", 25)
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "ask" {
		t.Fatalf("decision = %+v", d)
	}
}

func TestBoundaryHookFailsOpenOnABrokenConfig(t *testing.T) {
	home := boundaryHome(t, "boundaries: [ {name: x, action: deploy, require: ask} ]\n", 25)
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("a broken config refused the call: %+v", d)
	}
	envs := ledger(t, home)
	if len(envs) != 1 || envs[0].Payload.(*eventschema.DecisionEvent).Stage != eventschema.DecisionStageFailed {
		t.Fatalf("the failure was not recorded: %d entries", len(envs))
	}
}

func TestBoundariesPromoteAndDemote(t *testing.T) {
	boundaryHome(t, spendBoundary, 0)
	if out, err := runCLI(t, nil, "boundaries", "promote", "session-spend"); err != nil || !strings.Contains(out, "enforce") {
		t.Fatalf("promote = %q, %v", out, err)
	}
	if out, _ := runCLI(t, nil, "boundaries", "status"); !strings.Contains(out, "[enforce]") {
		t.Fatalf("status after promote:\n%s", out)
	}
	if out, err := runCLI(t, nil, "boundaries", "demote", "session-spend"); err != nil || !strings.Contains(out, "shadow") {
		t.Fatalf("demote = %q, %v", out, err)
	}
	if _, err := runCLI(t, nil, "boundaries", "promote", "nope"); err == nil {
		t.Fatal("promoted a rule that does not exist")
	}
}

func TestSubagentBoundaryRefusesBeforeAnyMove(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "0")
	boundaryHome(t, `boundaries:
  - name: big-subagents
    action: subagent_model
    when: { model_tier: "> sonnet" }
    require: deny
    stage: enforce
`, 0)
	out := runSubagentHook(t, t.TempDir(), map[string]any{"model": "opus", "description": "d", "prompt": "p"}, "")
	var d hookDecision
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("no hook output: %q", out)
	}
	if d.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(d.HookSpecificOutput.PermissionDecisionReason, "big-subagents") {
		t.Fatalf("decision = %+v", d.HookSpecificOutput)
	}
	if out := runSubagentHook(t, t.TempDir(), map[string]any{"model": "sonnet", "description": "d", "prompt": "p"}, ""); strings.Contains(out, "big-subagents") {
		t.Fatalf("a sonnet subagent was refused: %s", out)
	}
}

func TestHooksInstallBoundary(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	if out, err := runCLI(t, nil, "hooks", "install", "--boundary", "--settings", settings); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(settings)
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Args []string `json:"args"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	groups := s.Hooks["PreToolUse"]
	if len(groups) != 1 || groups[0].Matcher != "*" || groups[0].Hooks[0].Args[0] != "boundary-guard" {
		t.Fatalf("PreToolUse = %+v", groups)
	}
	if _, ok := s.Hooks["Stop"]; ok {
		t.Fatal("--boundary alone also installed the coach")
	}
	if out, _ := runCLI(t, nil, "hooks", "status", "--settings", settings); !strings.Contains(out, "boundary-guard") {
		t.Fatalf("status does not show it:\n%s", out)
	}
	if _, err := runCLI(t, nil, "hooks", "uninstall", "--boundary", "--settings", settings); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(settings); strings.Contains(string(b), "boundary-guard") {
		t.Fatalf("uninstall left it:\n%s", b)
	}
}

func TestHooksInstallBoundaryRefusesClientsThatCannotRefuse(t *testing.T) {
	for _, client := range []string{"codex", "cursor"} {
		_, err := runCLI(t, nil, "hooks", "install", "--boundary", "--client", client, "--settings", filepath.Join(t.TempDir(), "hooks.json"))
		if err == nil || !strings.Contains(err.Error(), "advice only") {
			t.Errorf("%s: err = %v", client, err)
		}
	}
}

func TestHooksInstallBoundaryOpencodeKeepsIt(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCLI(t, nil, "hooks", "install", "--boundary", "--client", "opencode", "--settings", dir); err != nil {
		t.Fatal(err)
	}
	// A later install of other halves (a coach preset does this) keeps it.
	if _, err := runCLI(t, nil, "hooks", "install", "--read-guard", "--client", "opencode", "--settings", dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, opencodePluginName))
	if !strings.Contains(string(b), `run(["boundary-guard"`) || !strings.Contains(string(b), `run(["read-guard"`) {
		t.Fatalf("plugin:\n%s", b)
	}
	if strings.Count(string(b), `"tool.execute.before"`) != 1 {
		t.Fatal("opencode 1.x hooks object has the event key twice; one would be lost")
	}
}

// atTerminal stands in for a person at a terminal for the rest of the test.
func atTerminal(t *testing.T) {
	t.Helper()
	prev := approverAtTerminal
	approverAtTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { approverAtTerminal = prev })
}

func TestApproveConfirmShowsWhatWaitsAndNeedsAYes(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "0")
	boundaryHome(t, spendBoundary+"    stage: enforce\n", 25)
	h := runBoundaryHook(t).HookSpecificOutput
	id := strings.Fields(h.PermissionDecisionReason[strings.Index(h.PermissionDecisionReason, "tokenops approve ")+len("tokenops approve "):])[0]
	id = strings.Trim(id, "`,.")
	atTerminal(t)

	out, err := runCLI(t, []byte("\n"), "approve", id, "--confirm")
	if err != nil || !strings.Contains(out, `Boundary "session-spend"`) || !strings.Contains(out, "session cost $25.00") ||
		!strings.Contains(out, "Left waiting") {
		t.Fatalf("no answer = %q, %v", out, err)
	}
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("an unconfirmed approval let the agent on: %+v", d)
	}
	out, err = runCLI(t, []byte("y\n"), "approve", id, "--confirm", "--session")
	if err != nil || !strings.Contains(out, "the rest of the session") || !strings.Contains(out, "approved") {
		t.Fatalf("yes = %q, %v", out, err)
	}
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("after a confirmed approval: %+v", d)
	}
}

// approve's help says what it guarantees and what it does not, so an
// operator does not read the terminal check as a security boundary.
func TestApproveHelpStatesItsLimits(t *testing.T) {
	out, err := executeRoot(t, "approve", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"What this guarantees", "What it does not", "script -q /dev/null", "Recommended"} {
		if !strings.Contains(out, want) {
			t.Errorf("approve --help lacks %q", want)
		}
	}
	out, err = executeRoot(t, "deny", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "What this guarantees") {
		t.Error("deny --help carries approve's limits")
	}
}

func TestBoundaryHookAppliesAFolderRuleOnlyInItsFolder(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "0")
	home := boundaryHome(t, `boundaries:
  - name: acme-only
    action: continue
    when: { folder: "~/work/acme" }
    require: deny
    stage: enforce
`, 0)
	hook := func(cwd string) hookDecision {
		t.Helper()
		body, _ := json.Marshal(map[string]any{
			"session_id": "s1", "transcript_path": "/x/.claude/projects/-repo/s1.jsonl", "tool_use_id": "tu-1",
			"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}, "cwd": cwd,
		})
		out, err := runCLI(t, body, "boundary-guard")
		if err != nil {
			t.Fatal(err)
		}
		var d hookDecision
		if out != "" {
			if err := json.Unmarshal([]byte(out), &d); err != nil {
				t.Fatalf("hook output %q: %v", out, err)
			}
		}
		return d
	}
	if d := hook(filepath.Join(home, "work", "other")); d.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("outside the folder: %+v", d)
	}
	d := hook(filepath.Join(home, "work", "acme", "api", "secret-subdir"))
	if d.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(d.HookSpecificOutput.PermissionDecisionReason, "working in "+filepath.Join(home, "work", "acme")) {
		t.Fatalf("inside the folder: %+v", d)
	}
	for _, e := range ledger(t, home) {
		if b, _ := json.Marshal(e); strings.Contains(string(b), "secret-subdir") {
			t.Fatal("the session's working directory reached the decision record")
		}
	}
	status, err := runCLI(t, nil, "boundaries", "status")
	if err != nil || !strings.Contains(status, "when folder ~/work/acme") {
		t.Fatalf("status = %s, %v", status, err)
	}
}
