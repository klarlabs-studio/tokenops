package fireworks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 3, 14, 0, 0, 0, time.UTC)

// config.json can hold keys; only ssoAccountId is taken from it.
func TestIdentity(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".fireconnect")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", `{"apiKey":"fw_secret","ssoAccountId":"sso-org"}`)
	if a, u := Identity(home); a != "sso-org" || u != "" {
		t.Errorf("config only: %q %q", a, u)
	}
	write("minted-key.json", `{"keyId":"k1","userName":"accounts/acme/users/felix","displayName":"fireconnect-mbp"}`)
	if a, u := Identity(home); a != "acme" || u != "felix" {
		t.Errorf("minted: %q %q", a, u)
	}
	if a, u := Identity(t.TempDir()); a != "" || u != "" {
		t.Errorf("empty home: %q %q", a, u)
	}
}

// Nexus CLI (FireConnect renamed) keeps the same files in ~/.nexus; a
// fresh install has no ~/.fireconnect at all.
func TestIdentityReadsNexus(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".nexus")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "minted-key.json"), []byte(`{"userName":"accounts/acme/users/felix"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if a, u := Identity(home); a != "acme" || u != "felix" {
		t.Errorf("nexus: %q %q", a, u)
	}
	// A stale FireConnect directory beside it does not win.
	old := filepath.Join(home, ".fireconnect")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "minted-key.json"), []byte(`{"userName":"accounts/old/users/someone"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if a, u := Identity(home); a != "acme" || u != "felix" {
		t.Errorf("nexus beside fireconnect: %q %q", a, u)
	}
}

func TestEnvelopeCarriesNoKey(t *testing.T) {
	env := NewEnvelope(now, Reading{Scope: ScopeUser, AccountID: "acme", UsedUSD: 41.5, LimitUSD: 100})
	for k, v := range env.Attributes {
		if strings.Contains(v, "fw_") {
			t.Errorf("%s carries a key: %q", k, v)
		}
	}
	if env.Attributes["extra_usage_used"] != "41.50" || env.Attributes["extra_usage_limit"] != "100.00" ||
		env.Attributes["billing"] != "per_token" || env.Attributes["granularity"] != "quota_snapshot" {
		t.Errorf("attrs %v", env.Attributes)
	}
}

// The billing summary is the whole account's month, every member together.
// The envelope keeps that scope and the account, which is what headroom,
// the cards and the menu bar label the figure with.
func TestAccountReadingKeepsItsScope(t *testing.T) {
	env := NewEnvelope(now, Reading{Scope: ScopeAccount, AccountID: "acme-test", UsedUSD: 1000, LimitUSD: 4000})
	if env.Attributes["scope"] != "account" || env.Attributes["account_id"] != "acme-test" {
		t.Errorf("attrs %v", env.Attributes)
	}
	if env.Attributes["extra_usage_used"] != "1000.00" || env.Attributes["extra_usage_limit"] != "4000.00" {
		t.Errorf("the figures changed: %v", env.Attributes)
	}
}

type userReader struct{ account, user string }

func (r *userReader) Read(_ context.Context, account, user string, _ time.Time) (Reading, error) {
	r.account, r.user = account, user
	return Reading{Scope: ScopeUser, AccountID: account}, nil
}

// The configured user wins over the one Nexus CLI's files name; the
// account still comes from those files.
func TestPollerUsesTheConfiguredUser(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".nexus")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "minted-key.json"), []byte(`{"userName":"accounts/acme/users/felix"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &userReader{}
	NewPoller(nil, PollerOptions{Client: r, Home: home, UserID: "u-42", Now: func() time.Time { return now }}).Scan(context.Background())
	if r.account != "acme" || r.user != "u-42" {
		t.Errorf("read %q %q", r.account, r.user)
	}
	r = &userReader{}
	NewPoller(nil, PollerOptions{Client: r, Home: home, Now: func() time.Time { return now }}).Scan(context.Background())
	if r.user != "felix" {
		t.Errorf("without a configured user: %q", r.user)
	}
}
