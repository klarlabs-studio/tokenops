package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

func runCookieSetupCmd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := newVendorUsageSetupCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// The instructions are the feature. A flag cannot describe four clicks in
// browser devtools, which is why `enable --session-key` was the wrong shape
// for this source.
func TestSetupExplainsWhereToFindTheKey(t *testing.T) {
	out, _ := runCookieSetupCmd(t, "\n", "claude-usage-meter")
	for _, want := range []string{"claude.ai", "developer tools", "Cookies", "sessionKey"} {
		if !strings.Contains(out, want) {
			t.Errorf("instructions omit %q:\n%s", want, out)
		}
	}
}

// Writing a key that was never checked is what made the old path useless: a
// mistyped cookie produced no data and no error an operator would see.
// Nothing is written until Anthropic has accepted it.
func TestSetupWritesNothingWithoutAKey(t *testing.T) {
	path := seedConfig(t)
	out, err := runCookieSetupCmd(t, "\n", "claude-usage-meter", "--config-path", path)
	if err == nil {
		t.Fatal("an empty key should be refused")
	}
	if !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("the refusal should say nothing was written: %v", err)
	}
	if strings.Contains(out, "wrote ") {
		t.Errorf("config was written despite no key:\n%s", out)
	}
	cfg, rerr := config.ReadMutable(path)
	if rerr != nil {
		t.Fatalf("read back: %v", rerr)
	}
	if cfg.VendorUsage.ClaudeUsageMeter.Enabled {
		t.Error("the source was enabled without a verified key")
	}
}

// A key Anthropic rejects must fail here, naming the most likely cause:
// these cookies rotate, so a stale copy is the common case.
func TestSetupRefusesAKeyAnthropicRejects(t *testing.T) {
	path := seedConfig(t)
	_, err := runCookieSetupCmd(t, "sk-ant-sid-definitely-not-valid\n", "claude-usage-meter", "--config-path", path)
	if err == nil {
		t.Fatal("an invalid key should be refused")
	}
	if !strings.Contains(err.Error(), "not accepted") && !strings.Contains(err.Error(), "read input") {
		t.Errorf("unexpected failure mode: %v", err)
	}
	cfg, rerr := config.ReadMutable(path)
	if rerr != nil {
		t.Fatalf("read back: %v", rerr)
	}
	if cfg.VendorUsage.ClaudeUsageMeter.Enabled || cfg.VendorUsage.ClaudeUsageMeter.SessionKey != "" {
		t.Errorf("a rejected key was persisted: %+v", cfg.VendorUsage.ClaudeUsageMeter)
	}
}

func TestSetupRejectsAnotherSource(t *testing.T) {
	if _, err := runCookieSetupCmd(t, "", "cursor"); err == nil {
		t.Fatal("setup covers claude-usage-meter only")
	}
}

// The paste was the friction this command existed to explain. It now looks
// where the browser already keeps the cookie, and only asks when it cannot
// find one — the prompt is the fallback, not the path.
func TestSetupLooksInTheBrowserFirst(t *testing.T) {
	// The test home has no browser profiles, so this exercises the
	// fallback and proves the search ran before the prompt.
	out, _ := runCookieSetupCmd(t, "\n", "claude-usage-meter")
	search := strings.Index(out, "Looking for your claude.ai session")
	prompt := strings.Index(out, "Paste sessionKey")
	if search < 0 || prompt < 0 || search > prompt {
		t.Errorf("browser search did not precede the prompt:\n%s", out)
	}
}

// --paste is for a machine with no browser to read: a server, CI, or an
// operator who would rather not have their keychain touched.
func TestSetupPasteSkipsTheBrowser(t *testing.T) {
	out, _ := runCookieSetupCmd(t, "\n", "claude-usage-meter", "--paste")
	if strings.Contains(out, "Looking for your claude.ai session") {
		t.Errorf("--paste still searched the browser:\n%s", out)
	}
	if !strings.Contains(out, "Paste sessionKey") {
		t.Errorf("--paste did not prompt:\n%s", out)
	}
}
