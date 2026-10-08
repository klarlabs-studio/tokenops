package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/providersetup"
	"go.klarlabs.de/tokenops/internal/config"
)

// fakeVerify accepts one key for one provider; no vendor is called.
func fakeVerify(t *testing.T, id, good string) *[]string {
	t.Helper()
	var sent []string
	prev := verifyProvider
	verifyProvider = func(_ context.Context, got, key, scope string) ([]string, error) {
		sent = append(sent, got+"|"+key)
		if scope != "" {
			sent[len(sent)-1] += "|" + scope
		}
		if got != id || key != good {
			return nil, fmt.Errorf("%w (401)", providersetup.ErrRefused)
		}
		return []string{"balance: $7.25"}, nil
	}
	t.Cleanup(func() { verifyProvider = prev })
	return &sent
}

// Any registry provider read with a key is set up the same way: the key is
// read without echo, checked once, and only then stored.
func TestProviderSetupStoresAVerifiedKey(t *testing.T) {
	sent := fakeVerify(t, "deepseek", "sk-good")
	path := seedConfig(t)
	out, err := runCookieSetupCmd(t, "sk-good\n", "deepseek", "--config", path, "--no-restart")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(*sent) != 1 || (*sent)[0] != "deepseek|sk-good" {
		t.Errorf("verified with %v", *sent)
	}
	if !strings.Contains(out, "balance: $7.25") || strings.Contains(out, "sk-good") {
		t.Errorf("output shows the reading and never the key:\n%s", out)
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	if c := cfg.VendorUsage.Accounts.Credentials["deepseek"]; c.Key != "sk-good" || c.FromBrowser {
		t.Errorf("stored %+v", c)
	}
}

// --scope is verified with the key and stored beside it; a later setup
// without --scope keeps it, and --scope "" clears it. A provider whose
// reader takes no scope refuses one.
func TestProviderSetupStoresAScope(t *testing.T) {
	sent := fakeVerify(t, "kilo", "kk")
	path := seedConfig(t)
	if out, err := runCookieSetupCmd(t, "kk\n", "kilo", "--scope", "org_123", "--config", path, "--no-restart"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := runCookieSetupCmd(t, "kk\n", "kilo", "--config", path, "--no-restart"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(*sent) != 2 || (*sent)[0] != "kilo|kk|org_123" || (*sent)[1] != "kilo|kk|org_123" {
		t.Errorf("verified with %v", *sent)
	}
	cfg, _ := config.ReadMutable(path)
	if got := cfg.VendorUsage.Accounts.Scopes["kilo"]; got != "org_123" {
		t.Errorf("stored scope %q", got)
	}
	if out, err := runCookieSetupCmd(t, "kk\n", "kilo", "--scope", "", "--config", path, "--no-restart"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cfg, _ = config.ReadMutable(path)
	if _, ok := cfg.VendorUsage.Accounts.Scopes["kilo"]; ok || (*sent)[2] != "kilo|kk" {
		t.Errorf("cleared scope: %v, verified %v", cfg.VendorUsage.Accounts.Scopes, *sent)
	}

	fakeVerify(t, "deepseek", "sk")
	if _, err := runCookieSetupCmd(t, "sk\n", "deepseek", "--scope", "x", "--config", path, "--no-restart"); err == nil || !strings.Contains(err.Error(), "takes no scope") {
		t.Errorf("deepseek --scope = %v", err)
	}
}

func TestProviderSetupWritesNothingForARefusedKey(t *testing.T) {
	fakeVerify(t, "zai", "sk-good")
	path := seedConfig(t)
	out, err := runCookieSetupCmd(t, "sk-bad\n", "zai", "--config", path, "--no-restart")
	if err == nil || !strings.Contains(err.Error(), "Nothing was written") {
		t.Fatalf("err %v\n%s", err, out)
	}
	cfg, _ := config.ReadMutable(path)
	if len(cfg.VendorUsage.Accounts.Credentials) != 0 {
		t.Errorf("stored %+v", cfg.VendorUsage.Accounts.Credentials)
	}
}

func TestProviderSetupNamesWhatItCovers(t *testing.T) {
	_, err := runCookieSetupCmd(t, "", "acme")
	if err == nil || !strings.Contains(err.Error(), "openrouter") {
		t.Fatalf("unknown provider: %v", err)
	}
}
