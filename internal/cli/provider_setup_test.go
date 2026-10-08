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
	verifyProvider = func(_ context.Context, got, key string) ([]string, error) {
		sent = append(sent, got+"|"+key)
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

// A session whose cookies are not known by name is pasted: no browser is
// read, and the daemon is not told to re-read one.
func TestProviderSetupStoresAPastedSession(t *testing.T) {
	sent := fakeVerify(t, "t3chat", "sid=ok; x=1")
	path := seedConfig(t)
	out, err := runCookieSetupCmd(t, "Cookie: sid=ok; x=1\n", "t3chat", "--config", path, "--no-restart")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(*sent) != 1 || !strings.Contains(out, "Cookie header for t3.chat") || strings.Contains(out, "Keychain") {
		t.Errorf("sent %v, output:\n%s", *sent, out)
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	if c := cfg.VendorUsage.Accounts.Credentials["t3chat"]; c.Key != "sid=ok; x=1" || c.FromBrowser || c.Browser != "" {
		t.Errorf("stored %+v", c)
	}
}

func TestProviderSetupNamesWhatItCovers(t *testing.T) {
	_, err := runCookieSetupCmd(t, "", "acme")
	if err == nil || !strings.Contains(err.Error(), "openrouter") {
		t.Fatalf("unknown provider: %v", err)
	}
}
