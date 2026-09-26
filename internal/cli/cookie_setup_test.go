package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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

// meterServing points the verification request at a local server for the
// duration of one test, so no test in this file reaches claude.ai.
func meterServing(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	prev := meterBaseURL
	meterBaseURL = ts.URL
	t.Cleanup(func() { meterBaseURL = prev })
}

// A key Anthropic rejects must fail here, naming the most likely cause:
// these cookies rotate, so a stale copy is the common case.
func TestSetupRefusesAKeyAnthropicRejects(t *testing.T) {
	meterServing(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
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

// Cloudflare's bot check is a different failure from a rejected key, and
// conflating them is what sent an operator to look at their plan instead of
// at the request. The advice must name the fix: load claude.ai in the
// browser the session came from, which renews cf_clearance.
func TestSetupExplainsTheBotCheck(t *testing.T) {
	meterServing(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html><title>Just a moment...</title></html>"))
	})
	path := seedConfig(t)
	_, err := runCookieSetupCmd(t, "sk-ant-sid-whatever\n", "claude-usage-meter", "--config-path", path)
	if err == nil {
		t.Fatal("a refused request should be reported")
	}
	if strings.Contains(err.Error(), "not accepted") {
		t.Errorf("a bot check was reported as a bad key: %v", err)
	}
	if !strings.Contains(err.Error(), "claude.ai") {
		t.Errorf("the advice should name claude.ai as the fix: %v", err)
	}
	cfg, rerr := config.ReadMutable(path)
	if rerr != nil {
		t.Fatalf("read back: %v", rerr)
	}
	if cfg.VendorUsage.ClaudeUsageMeter.Enabled || cfg.VendorUsage.ClaudeUsageMeter.SessionKey != "" {
		t.Errorf("a key was persisted despite a refused request: %+v", cfg.VendorUsage.ClaudeUsageMeter)
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

func TestParseCopiedClaudeUsageRequest(t *testing.T) {
	raw := `curl 'https://claude.ai/api/organizations/org-public-test/usage' ` +
		`-H 'cookie: other=x; sessionKey=sk-ant-sid-test; cf_clearance=clearance-test' ` +
		`-H 'user-agent: Mozilla/5.0 Chrome/141.0.0.0' -H 'x-unrelated: discarded'`
	session, err := parseUsageRequest(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if session.key != "sk-ant-sid-test" || session.clearance != "clearance-test" {
		t.Fatalf("cookies not reduced correctly: %+v", session)
	}
	if session.userAgent != "Mozilla/5.0 Chrome/141.0.0.0" || session.orgID != "org-public-test" {
		t.Fatalf("request metadata not extracted correctly: %+v", session)
	}
}

func TestParseCopiedUsageRequestSupportsCurlCookieFlag(t *testing.T) {
	raw := `curl "https://claude.ai/api/organizations/org-test/usage" ` +
		`--cookie "sessionKey=sk-ant-sid-test; cf_clearance=clearance-test"`
	session, err := parseUsageRequest(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if session.key != "sk-ant-sid-test" || session.clearance != "clearance-test" {
		t.Fatalf("cookie flag not parsed: %+v", session)
	}
}

func TestParseCopiedUsageRequestRejectsOtherEndpoints(t *testing.T) {
	for _, raw := range []string{
		`curl 'https://example.com/api/organizations/org/usage' -H 'cookie: sessionKey=secret'`,
		`curl 'https://claude.ai/api/organizations/org/chat' -H 'cookie: sessionKey=secret'`,
		`curl 'https://claude.ai/api/organizations/org/usage' -H 'cookie: other=value'`,
	} {
		if _, err := parseUsageRequest(raw); err == nil || !strings.Contains(err.Error(), "nothing was written") {
			t.Errorf("unsafe request was not rejected clearly: %q: %v", raw, err)
		}
	}
}

func TestSetupPasteRequestPersistsOnlyBoundedSessionFields(t *testing.T) {
	meterServing(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/organizations" {
			_, _ = w.Write([]byte(`[{"uuid":"org-test","name":"Test"}]`))
			return
		}
		_, _ = w.Write([]byte(`{"limits":[{"kind":"session","percent":12,"resets_at":"2030-01-01T00:00:00Z"}]}`))
	})
	path := seedConfig(t)
	raw := `curl 'https://claude.ai/api/organizations/org-test/usage' -H 'cookie: sessionKey=sk-ant-sid-test; cf_clearance=clearance-test' -H 'user-agent: Test Browser'`
	out, err := runCookieSetupCmd(t, raw+"\n", "claude-usage-meter", "--paste-request", "--config-path", path, "--no-restart")
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	got := cfg.VendorUsage.ClaudeUsageMeter
	if !got.Enabled || got.SessionKey != "sk-ant-sid-test" || got.Clearance != "clearance-test" || got.UserAgent != "Test Browser" || got.OrgID != "org-test" {
		t.Fatalf("bounded session fields not persisted: %+v", got)
	}
	if got.FromBrowser {
		t.Error("pasted request should not claim browser refresh access")
	}
}

// Cloudflare's clearance cookie is short-lived and renewed by loading the
// page, so "sign in again" sends the operator to the wrong place.
func TestBotCheckErrorSaysHowToRenewIt(t *testing.T) {
	err := botCheckAdvice("Chrome")
	for _, want := range []string{"claude.ai", "Chrome", "renews"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("advice %q omits %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "sign in again") {
		t.Errorf("advice blames the session: %q", err)
	}
}
