package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
)

// setup claude-code reads Claude Code's sign-in, proves it reads usage,
// and writes only the switch: never the token. --no-keychain keeps this
// test away from the real Keychain.
func TestClaudeCodeSetup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := seedConfig(t)

	out, err := runCookieSetupCmd(t, "", "claude-code", "--config", path, "--no-keychain", "--no-restart")
	if err == nil || !strings.Contains(err.Error(), "not signed in") || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("no sign-in: %v\n%s", err, out)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":10,"resets_at":"2026-10-06T15:00:00Z"},"seven_day":{"utilization":22,"resets_at":"2026-10-09T23:00:00Z"}}`))
	}))
	defer srv.Close()
	claudeCodeBaseURL = srv.URL
	t.Cleanup(func() { claudeCodeBaseURL = "" })
	creds := filepath.Join(home, ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(creds), 0o700); err != nil {
		t.Fatal(err)
	}
	expires := strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10)
	if err := os.WriteFile(creds, []byte(`{"claudeAiOauth":{"accessToken":"fake-token","expiresAt":`+expires+`,"scopes":["user:profile"],"subscriptionType":"max"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err = runCookieSetupCmd(t, "", "claude-code", "--config", path, "--no-keychain", "--no-restart")
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Plan: Max") || strings.Contains(out, "fake-token") {
		t.Errorf("output:\n%s", out)
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.VendorUsage.ClaudeCodeOAuth.Enabled || cfg.VendorUsage.ClaudeCodeOAuth.Keychain {
		t.Errorf("config %+v", cfg.VendorUsage.ClaudeCodeOAuth)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "fake-token") {
		t.Error("the token was written to the config")
	}
}
