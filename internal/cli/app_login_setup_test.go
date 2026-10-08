package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/providersetup"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/applogin"
	"go.klarlabs.de/tokenops/internal/infra/keychain"
)

const cliAppToken = "tok-CLI-APP-SECRET"

// fakeAppLoginHome points setup at a temporary home holding the kilo CLI's
// sign-in, and replaces the vendor check: no real file and no vendor is
// touched. It returns the sign-in's path and the providers checked.
func fakeAppLoginHome(t *testing.T, refuse bool) (string, *[]string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ".local/share/kilo/auth.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"kilo":{"access":"`+cliAppToken+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prevEnv, prevVerify := appLoginEnv, verifyAppLogin
	appLoginEnv = func(disabled bool) applogin.Env {
		return applogin.Env{Home: home, Getenv: func(string) string { return "" }, KeychainDisabled: disabled,
			Keychain: func(i keychain.Item) (string, error) {
				t.Errorf("setup read the Keychain item %s", i)
				return "", keychain.ErrNotFound
			}}
	}
	var checked []string
	verifyAppLogin = func(_ context.Context, id string, _ providersetup.AppLogin, _ applogin.Env) ([]string, error) {
		checked = append(checked, id)
		if refuse {
			return nil, fmt.Errorf("%w (401)", providersetup.ErrRefused)
		}
		return []string{"balance: $19.00"}, nil
	}
	t.Cleanup(func() { appLoginEnv, verifyAppLogin = prevEnv, prevVerify })
	return path, &checked
}

// Setup says exactly what it reads and where it goes, asks, checks once,
// and only then records a grant that holds no secret.
func TestAppLoginSetupShowsAsksAndGrants(t *testing.T) {
	item, checked := fakeAppLoginHome(t, false)
	path := seedConfig(t)
	out, err := runCookieSetupCmd(t, "y\n", "kilo", "--use-app-login", "--config", path, "--no-restart")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{item, "kilo.access", "app.kilo.ai", "the kilo CLI", "[y/N]", "balance: $19.00", "--revoke-app-login"} {
		if !strings.Contains(out, want) {
			t.Errorf("output omits %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, cliAppToken) || len(*checked) != 1 {
		t.Errorf("token shown, or checked %v:\n%s", *checked, out)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), cliAppToken) {
		t.Error("the config holds the token")
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	g := cfg.VendorUsage.Grants["kilo"]
	if g.Item != item || g.Kind != "json-file" || g.Host != "app.kilo.ai" || len(g.Fields) != 1 || g.GrantedAt.IsZero() {
		t.Errorf("grant %+v", g)
	}

	// Revoking removes it.
	out, err = runCookieSetupCmd(t, "", "kilo", "--revoke-app-login", "--config", path, "--no-restart")
	if err != nil || !strings.Contains(out, "Revoked") {
		t.Fatalf("%v\n%s", err, out)
	}
	cfg, _ = config.ReadMutable(path)
	if len(cfg.VendorUsage.Grants) != 0 {
		t.Errorf("still granted: %+v", cfg.VendorUsage.Grants)
	}
}

// Anything but yes reads nothing and writes nothing.
func TestAppLoginSetupDeclinedReadsNothing(t *testing.T) {
	_, checked := fakeAppLoginHome(t, false)
	for _, answer := range []string{"\n", "n\n", "no\n", ""} {
		path := seedConfig(t)
		out, err := runCookieSetupCmd(t, answer, "kilo", "--use-app-login", "--config", path, "--no-restart")
		if err == nil || !strings.Contains(err.Error(), "not granted") {
			t.Errorf("%q: %v\n%s", answer, err, out)
		}
		cfg, _ := config.ReadMutable(path)
		if len(cfg.VendorUsage.Grants) != 0 {
			t.Errorf("%q: granted", answer)
		}
	}
	if len(*checked) != 0 {
		t.Errorf("read the sign-in without a yes: %v", *checked)
	}
}

// A sign-in the vendor refuses is not granted.
func TestAppLoginSetupRefusedWritesNothing(t *testing.T) {
	fakeAppLoginHome(t, true)
	path := seedConfig(t)
	out, err := runCookieSetupCmd(t, "y\n", "kilo", "--use-app-login", "--config", path, "--no-restart")
	if err == nil || !strings.Contains(err.Error(), "Nothing was written") || strings.Contains(out+err.Error(), cliAppToken) {
		t.Fatalf("%v\n%s", err, out)
	}
	cfg, _ := config.ReadMutable(path)
	if len(cfg.VendorUsage.Grants) != 0 {
		t.Errorf("granted: %+v", cfg.VendorUsage.Grants)
	}
}

// A provider read only with another app's sign-in says how, rather than
// asking for an API key it does not have.
func TestAppLoginOnlyProviderPointsAtTheFlag(t *testing.T) {
	for _, id := range providersetup.IDs() {
		if p, _ := providersetup.Lookup(id); p.AppLoginOnly {
			_, err := runCookieSetupCmd(t, "", id, "--no-restart")
			if err == nil || !strings.Contains(err.Error(), "--use-app-login") {
				t.Errorf("%s: %v", id, err)
			}
		}
	}
	_, err := runCookieSetupCmd(t, "", "openrouter", "--use-app-login", "--no-restart")
	if err == nil || !strings.Contains(err.Error(), "not read with another application's sign-in") {
		t.Errorf("%v", err)
	}
}

// Status lists each grant, what it reads and where it goes, never a token.
func TestStatusRendersGrants(t *testing.T) {
	cfg := config.Default()
	cfg.VendorUsage.Grants = map[string]config.AppLoginGrant{"kilo": {
		App: "the kilo CLI", Kind: "json-file", Item: "/home/me/.local/share/kilo/auth.json",
		Fields: []string{"kilo.access"}, Host: "app.kilo.ai",
	}}
	rows := make([]vendorUsageGrant, 0, 1)
	for _, g := range providersetup.Grants(cfg, providersetup.AppLoginEnv(cfg.Keychain.Disabled)) {
		rows = append(rows, vendorUsageGrant{Provider: g.Provider, App: g.App, Reads: g.What, SentTo: g.Host, Current: g.Current})
	}
	var b strings.Builder
	renderGrants(&b, rows)
	for _, want := range []string{"kilo", "the kilo CLI", "auth.json", "kilo.access", "app.kilo.ai"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("omits %q:\n%s", want, b.String())
		}
	}
}
