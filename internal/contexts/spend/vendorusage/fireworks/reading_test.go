package fireworks

import (
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
