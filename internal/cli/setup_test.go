package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// The summary has to distinguish work done from state already correct, and
// it has to name what it deliberately did not do. A setup command that
// reports only its successes is how a tool ends up half-wired.
func TestRenderSetupSeparatesDoneFromOwed(t *testing.T) {
	var buf bytes.Buffer
	renderSetup(&buf, []setupStep{
		{Name: "MCP: Claude Code", Changed: true, Detail: "registered"},
		{Name: "Claude Code hooks", Detail: "already wired"},
		{Name: "plan binding", Manual: true, Detail: "pick your tier"},
	})
	out := buf.String()
	for _, want := range []string{
		"✓ MCP: Claude Code",
		"= Claude Code hooks",
		"· plan binding",
		"1 step(s) still need you",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// A fully wired machine must report nothing outstanding.
func TestRenderSetupQuietWhenComplete(t *testing.T) {
	var buf bytes.Buffer
	renderSetup(&buf, []setupStep{{Name: "MCP: Claude Code", Detail: "already correct"}})
	if strings.Contains(buf.String(), "still need you") {
		t.Errorf("clean run should claim nothing outstanding:\n%s", buf.String())
	}
}

// Detection knows a client is installed but not which tier is paid for, and
// the tiers differ by 4x in headroom. Binding one anyway would make every
// headroom figure confidently wrong, so it must stay manual.
func TestBindPlanRefusesToGuessTier(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("listen: 127.0.0.1:7878\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	step := bindPlan(cfgPath)
	if step.Err != nil {
		t.Fatalf("bindPlan: %v", step.Err)
	}
	if !step.Manual && step.Detail != "" && !strings.Contains(step.Detail, "already bound") {
		t.Errorf("an unbound plan should be reported as manual, got %+v", step)
	}
}

// An already-bound plan is left alone and reported as such.
func TestBindPlanLeavesExistingBinding(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath,
		[]byte("listen: 127.0.0.1:7878\nplans:\n    anthropic: claude-max-20x\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	step := bindPlan(cfgPath)
	if step.Manual || step.Err != nil {
		t.Fatalf("existing binding should be a no-op: %+v", step)
	}
	if !strings.Contains(step.Detail, "claude-max-20x") {
		t.Errorf("detail should name the bound plan: %q", step.Detail)
	}
}

// Wiring MCP is idempotent: the second run reports no change.
func TestWireMCPHostsIdempotent(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	first := wireMCPHosts(home, "/opt/bin/tokenops")
	if len(first) != 1 || !first[0].Changed {
		t.Fatalf("first run should register: %+v", first)
	}
	second := wireMCPHosts(home, "/opt/bin/tokenops")
	if len(second) != 1 || second[0].Changed {
		t.Errorf("second run should be a no-op: %+v", second)
	}
}

// The point of the whole exercise: the registered command must be the
// absolute path of this binary, never a bare name resolved through PATH.
func TestWireMCPHostsPinsAbsolutePath(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, ".claude.json")
	seed := `{"mcpServers":{"tokenops":{"command":"tokenops","args":["serve"]}}}`
	if err := os.WriteFile(cfgPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	steps := wireMCPHosts(home, "/exact/path/tokenops")
	if len(steps) != 1 || !steps[0].Changed {
		t.Fatalf("a bare command must be repointed: %+v", steps)
	}
	if !strings.Contains(steps[0].Detail, "restart") {
		t.Errorf("repoint must tell the operator to restart the host: %q", steps[0].Detail)
	}
	b, _ := os.ReadFile(cfgPath)
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("parse: %v", err)
	}
	entry := root["mcpServers"].(map[string]any)["tokenops"].(map[string]any)
	if entry["command"] != "/exact/path/tokenops" {
		t.Errorf("command = %v, want the absolute path", entry["command"])
	}
}

// No host installed is a fact to report, not a silent success.
func TestWireMCPHostsReportsNoHosts(t *testing.T) {
	steps := wireMCPHosts(t.TempDir(), "/opt/bin/tokenops")
	if len(steps) != 1 || !steps[0].Manual {
		t.Fatalf("absent hosts should be reported as manual: %+v", steps)
	}
}

// Regression: runSetup must write only inside the target it was given.
// An earlier version resolved $HOME internally, so running the CLI tests
// repointed the developer's live MCP entry at a go-build test binary.
func TestRunSetupTouchesOnlyItsTarget(t *testing.T) {
	sandbox := t.TempDir()
	hostCfg := filepath.Join(sandbox, ".claude.json")
	if err := os.WriteFile(hostCfg, []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	settings := filepath.Join(sandbox, ".claude", "settings.json")
	cfgPath := filepath.Join(sandbox, "config.yaml")
	if err := os.WriteFile(cfgPath,
		[]byte("listen: 127.0.0.1:7878\nplans:\n    anthropic: claude-max-20x\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	// A canary outside the sandbox: if runSetup resolves its own paths it
	// writes here instead, and the assertions below miss it — so assert on
	// the sandbox contents directly.
	var buf bytes.Buffer
	runSetup(&buf, cfgPath, setupTarget{
		Home:         sandbox,
		SettingsPath: settings,
		Exe:          "/sandbox/bin/tokenops",
	})

	root := map[string]any{}
	b, err := os.ReadFile(hostCfg)
	if err != nil {
		t.Fatalf("read host: %v", err)
	}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("parse host: %v", err)
	}
	entry, ok := root["mcpServers"].(map[string]any)["tokenops"].(map[string]any)
	if !ok {
		t.Fatalf("tokenops not registered in the sandbox host: %v", root)
	}
	if entry["command"] != "/sandbox/bin/tokenops" {
		t.Errorf("command = %v, want the injected exe", entry["command"])
	}
	if _, err := os.Stat(settings); err != nil {
		t.Errorf("hooks were not written to the injected settings path: %v", err)
	}
	if !strings.Contains(buf.String(), "already bound") {
		t.Errorf("expected the seeded plan to be reported:\n%s", buf.String())
	}
}

// writeHomeWithEvidence is a home where Claude Code reports Max 20x and
// Codex reports Plus.
func writeHomeWithEvidence(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"),
		[]byte(`{"oauthAccount":{"organizationType":"claude_max","organizationRateLimitTier":"default_claude_max_20x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".codex", "sessions", "2026", "10", "03")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"timestamp":"2026-10-03T08:45:14Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}},"rate_limits":{"plan_type":"plus"}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "rollout-1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// What the clients report is bound without asking, and says where it came
// from; a second run changes nothing.
func TestBindPlansFromWhatTheClientsReport(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("listen: 127.0.0.1:7878\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home := writeHomeWithEvidence(t)
	steps := bindPlans(cfgPath, home)
	if len(steps) != 2 {
		t.Fatalf("steps %+v", steps)
	}
	for _, s := range steps {
		if !s.Changed || s.Manual || s.Err != nil || !strings.Contains(s.Detail, "reports") {
			t.Errorf("step %+v", s)
		}
	}
	cfg, err := config.ReadMutable(cfgPath)
	if err != nil || cfg.Plans["anthropic"] != "claude-max-20x" || cfg.Plans["openai"] != "gpt-plus" {
		t.Fatalf("plans %v, %v", cfg.Plans, err)
	}
	for _, s := range bindPlans(cfgPath, home) {
		if s.Changed || s.Manual {
			t.Errorf("second run changed something: %+v", s)
		}
	}
}

// A plan the operator bound is theirs: a client reporting another one is
// pointed out, not applied.
func TestBindPlansNeverOverridesTheOperator(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("listen: 127.0.0.1:7878\nplans:\n    anthropic: claude-pro\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var disagree *setupStep
	for _, s := range bindPlans(cfgPath, writeHomeWithEvidence(t)) {
		if strings.HasPrefix(s.Detail, "anthropic is bound") {
			s := s
			disagree = &s
		}
	}
	if disagree == nil || !disagree.Manual || !strings.Contains(disagree.Detail, "claude-max-20x") {
		t.Fatalf("disagreement not pointed out: %+v", disagree)
	}
	if cfg, _ := config.ReadMutable(cfgPath); cfg.Plans["anthropic"] != "claude-pro" {
		t.Errorf("the operator's plan was replaced: %v", cfg.Plans)
	}
}
