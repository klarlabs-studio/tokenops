package planevidence

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
		{`{"organizationType":"claude_max","organizationRateLimitTier":"default_claude_max_20x"}`, "claude-ultra", 0},
		{`{"organizationType":"claude_max","organizationRateLimitTier":"default_claude_max_5x"}`, "claude-max", 0},
		// Guesses at what Claude Code reports once it follows the rename of
		// Max 20x to Ultra; it still said max_20x on 2026-10-31.
		{`{"organizationType":"claude_max","organizationRateLimitTier":"default_claude_ultra"}`, "claude-ultra", 0},
		{`{"organizationType":"claude_ultra"}`, "claude-ultra", 0},
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

// Evidence names the client that reported it, as the audit log's actor,
// and what it reported field by field, so a plan TokenOps follows can be
// traced back to the exact account type and tier.
func TestEvidenceNamesItsClientAndFacts(t *testing.T) {
	e, ok := Claude(Home(t, `{"organizationType":"claude_max","organizationRateLimitTier":"default_claude_max_5x"}`, ""))
	if !ok || e.Client != "claude_code" || e.Facts["account_type"] != "claude_max" ||
		e.Facts["rate_limit_tier"] != "default_claude_max_5x" {
		t.Errorf("claude evidence %+v", e)
	}
	c, ok := Codex(filepath.Join(Home(t, "", "pro"), ".codex", "sessions"))
	if !ok || c.Client != "codex" || c.Facts["plan_type"] != "pro" {
		t.Errorf("codex evidence %+v", c)
	}
}

// TestReaderFollowsTheNewestSessions reads the same sessions again and
// again as the daemon does, and requires each answer to be the one a
// fresh read gives: an appended turn, a newer session, a session gone.
func TestReaderFollowsTheNewestSessions(t *testing.T) {
	home := Home(t, "", "plus")
	root := filepath.Join(home, ".codex", "sessions")
	dir := filepath.Join(root, "2026", "10", "03")
	first := filepath.Join(dir, "rollout-1.jsonl")
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	write := func(path, planType string, appendTo bool) {
		t.Helper()
		flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		if appendTo {
			flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		}
		f, err := os.OpenFile(path, flag, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(replace(codexTurn, planType) + "\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Minute)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	steps := []struct {
		name string
		do   func()
		want string
	}{
		{"first read", func() {}, "gpt-plus"},
		{"unchanged", func() {}, "gpt-plus"},
		{"a turn appended", func() { write(first, "pro", true) }, "gpt-pro-20x"},
		{"a newer session", func() { write(filepath.Join(dir, "rollout-2.jsonl"), "plus", false) }, "gpt-plus"},
		{"the newer one gone", func() {
			if err := os.Remove(filepath.Join(dir, "rollout-2.jsonl")); err != nil {
				t.Fatal(err)
			}
		}, "gpt-pro-20x"},
	}
	r := &Reader{}
	for _, s := range steps {
		s.do()
		got, ok := r.Codex(root)
		fresh, freshOK := Codex(root)
		if !ok || got.Plan != s.want || got.Detail != fresh.Detail || ok != freshOK {
			t.Fatalf("%s: kept %+v (%v), fresh %+v (%v), want plan %s", s.name, got, ok, fresh, freshOK, s.want)
		}
	}
}
