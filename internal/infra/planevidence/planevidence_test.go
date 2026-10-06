package planevidence

import (
	"os"
	"path/filepath"
	"testing"
)

const codexTurn = `{"timestamp":"2026-04-17T08:45:14.725Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12},"last_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12},"model_context_window":258400},"rate_limits":{"primary":{"used_percent":10.0,"window_minutes":300,"resets_at":1776431887},"plan_type":"%s"}}}`

// Home builds a home whose clients report the given plans.
func Home(t *testing.T, claudeAccount, codexPlanType string) string {
	t.Helper()
	home := t.TempDir()
	if claudeAccount != "" {
		if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"oauthAccount":`+claudeAccount+`,"emailAddress":"x"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if codexPlanType != "" {
		dir := filepath.Join(home, ".codex", "sessions", "2026", "10", "03")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		line := []byte(replace(codexTurn, codexPlanType) + "\n")
		if err := os.WriteFile(filepath.Join(dir, "rollout-1.jsonl"), line, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func replace(format, v string) string {
	out := []byte{}
	for i := 0; i < len(format); i++ {
		if format[i] == '%' && i+1 < len(format) && format[i+1] == 's' {
			out = append(out, v...)
			i++
			continue
		}
		out = append(out, format[i])
	}
	return string(out)
}

func TestClaudeAccountTiers(t *testing.T) {
	for _, tc := range []struct {
		account, plan string
		candidates    int
	}{
		{`{"organizationType":"claude_max","organizationRateLimitTier":"default_claude_max_20x"}`, "claude-max-20x", 0},
		{`{"organizationType":"claude_max","organizationRateLimitTier":"default_claude_max_5x"}`, "claude-max-5x", 0},
		{`{"organizationType":"claude_max"}`, "", 2},
		{`{"organizationType":"claude_pro"}`, "claude-pro", 0},
		{`{"organizationType":"claude_enterprise"}`, "claude-enterprise", 0},
		{`{"organizationType":"claude_team","seatTier":"premium"}`, "claude-team-premium", 0},
		{`{"organizationType":"claude_team"}`, "", 2},
	} {
		e, ok := Claude(Home(t, tc.account, ""))
		if !ok || e.Plan != tc.plan || len(e.Candidates) != tc.candidates || e.Detail == "" {
			t.Errorf("%s: %+v %v", tc.account, e, ok)
		}
	}
	if _, ok := Claude(Home(t, `{}`, "")); ok {
		t.Error("an account without a type (an API key) named a plan")
	}
	if _, ok := Claude(t.TempDir()); ok {
		t.Error("no Claude Code named a plan")
	}
}

func TestCodexPlanType(t *testing.T) {
	e, ok := Codex(filepath.Join(Home(t, "", "plus"), ".codex", "sessions"))
	if !ok || e.Plan != "gpt-plus" || e.Detail != "plan_type plus" {
		t.Fatalf("got %+v %v", e, ok)
	}
	unknown, ok := Codex(filepath.Join(Home(t, "", "galaxy"), ".codex", "sessions"))
	if !ok || unknown.Plan != "" {
		t.Errorf("an unknown plan_type named a plan: %+v", unknown)
	}
}

func TestAccountsNamesTheClaudeSignIn(t *testing.T) {
	home := t.TempDir()
	if got := Accounts(home); len(got) != 0 {
		t.Fatalf("no ~/.claude.json, got %v", got)
	}
	body := `{"emailAddress":"top-level@x","oauthAccount":{"organizationType":"claude_max","emailAddress":"me@example.com"}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Accounts(home); got["anthropic"] != "me@example.com" || len(got) != 1 {
		t.Errorf("got %v", got)
	}
}
