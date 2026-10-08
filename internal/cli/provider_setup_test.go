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

func TestProviderSetupNamesWhatItCovers(t *testing.T) {
	_, err := runCookieSetupCmd(t, "", "acme")
	if err == nil || !strings.Contains(err.Error(), "openrouter") {
		t.Fatalf("unknown provider: %v", err)
	}
}

// A gateway is set up with its address and a key, checked once there, and
// only then stored.
func TestProviderSetupStoresAGatewayAddress(t *testing.T) {
	var sent []string
	prev := verifyGateway
	verifyGateway = func(_ context.Context, id, base, key string) ([]string, error) {
		sent = append(sent, id+"|"+base+"|"+key)
		if key != "vk" {
			return nil, fmt.Errorf("%w (401)", providersetup.ErrRefused)
		}
		return []string{"spend: $2.00 of $10.00"}, nil
	}
	t.Cleanup(func() { verifyGateway = prev })
	path := seedConfig(t)
	out, err := runCookieSetupCmd(t, "https://s2.example\nvk\n", "sub2api", "--config", path, "--no-restart")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(sent) != 1 || sent[0] != "sub2api|https://s2.example|vk" {
		t.Errorf("verified with %v", sent)
	}
	if !strings.Contains(out, "spend: $2.00") || strings.Contains(out, "vk\n") {
		t.Errorf("output:\n%s", out)
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	if c := cfg.VendorUsage.Accounts.Credentials["sub2api"]; c.Key != "vk" || c.BaseURL != "https://s2.example" {
		t.Errorf("stored %+v", c)
	}

	path = seedConfig(t)
	if _, err := runCookieSetupCmd(t, "https://s2.example\nbad\n", "sub2api", "--config", path, "--no-restart"); err == nil || !strings.Contains(err.Error(), "Nothing was written") {
		t.Fatalf("refused key: %v", err)
	}
	if cfg, _ := config.ReadMutable(path); len(cfg.VendorUsage.Accounts.Credentials) != 0 {
		t.Errorf("stored %+v", cfg.VendorUsage.Accounts.Credentials)
	}
}
