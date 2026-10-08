package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/providersetup"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/infra/applogin"
	"go.klarlabs.de/tokenops/internal/infra/keychain"
	accountsapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/accounts"
)

const appToken = "tok-APP-LOGIN-SECRET"

// sealedEnv is a temporary home where every file sign-in any descriptor
// names exists, holding appToken, and where any Keychain or process read
// fails the test.
func sealedEnv(t *testing.T) applogin.Env {
	t.Helper()
	home := t.TempDir()
	for _, d := range providers.All() {
		s, ok := d.AppLoginSource()
		if !ok {
			continue
		}
		for _, a := range s.AppLogins {
			for _, p := range a.Paths {
				path := filepath.Join(home, strings.TrimPrefix(p, "~/"))
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(appToken), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return applogin.Env{
		Home:   home,
		Getenv: func(string) string { return "" },
		Keychain: func(i keychain.Item) (string, error) {
			t.Errorf("read the Keychain item %s", i)
			return "", keychain.ErrNotFound
		},
		Processes: func(context.Context) ([]applogin.Process, error) {
			t.Error("listed processes")
			return nil, nil
		},
	}
}

func writeKiloLogin(t *testing.T, home, token string) string {
	t.Helper()
	path := filepath.Join(home, ".local/share/kilo/auth.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"kilo":{"access":"`+token+`","refresh":"never-read"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func kiloGrant(item string) config.AppLoginGrant {
	return config.AppLoginGrant{App: "the kilo CLI", Kind: "json-file", Item: item,
		Fields: []string{"kilo.access"}, Host: "app.kilo.ai", GrantedAt: time.Now()}
}

// With no grant, no other application's sign-in is a credential: not a
// file, not a Keychain item, not a process.
func TestNoAppLoginIsReadWithoutAGrant(t *testing.T) {
	env := sealedEnv(t)
	cfg := config.Default()
	if got := grantedCredentials(cfg, accountsapi.Readers(), env); len(got) != 0 {
		t.Fatalf("credentials without a grant: %+v", got)
	}
}

// A granted sign-in is read afresh on every resolve, so the owner's renewed
// token is picked up, and the file is never written. Revoking stops it.
func TestGrantedAppLoginIsReadAfreshUntilRevoked(t *testing.T) {
	env := sealedEnv(t)
	path := writeKiloLogin(t, env.Home, "t1")
	cfg := config.Default()
	cfg.VendorUsage.Grants = map[string]config.AppLoginGrant{"kilo": kiloGrant(path)}
	creds := grantedCredentials(cfg, accountsapi.Readers(), env)
	if len(creds) != 1 || creds[0].Endpoint != "kilo" || !creds[0].AppLogin || creds[0].Key != "" {
		t.Fatalf("%+v", creds)
	}
	if got, err := creds[0].Resolve(context.Background()); err != nil || got != "t1" {
		t.Fatalf("%q %v", got, err)
	}
	writeKiloLogin(t, env.Home, "t2")
	before, _ := os.ReadFile(path)
	if got, err := creds[0].Resolve(context.Background()); err != nil || got != "t2" {
		t.Fatalf("renewed token: %q %v", got, err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("the sign-in file was written")
	}
	if strings.Contains(creds[0].Origin, "t2") {
		t.Error("the origin names the token")
	}
	providersetup.RevokeGrant(&cfg, "kilo")
	if got := grantedCredentials(cfg, accountsapi.Readers(), env); len(got) != 0 {
		t.Errorf("revoked, still read: %+v", got)
	}
}

// A grant covers only what was shown: another item, field or host, or a
// provider whose descriptor no longer reads it, is not read.
func TestAGrantCoversExactlyWhatWasShown(t *testing.T) {
	env := sealedEnv(t)
	path := writeKiloLogin(t, env.Home, "t1")
	for name, g := range map[string]config.AppLoginGrant{
		"another file":  kiloGrant(filepath.Join(env.Home, ".ssh/id_ed25519")),
		"another field": func() config.AppLoginGrant { g := kiloGrant(path); g.Fields = []string{"kilo.refresh"}; return g }(),
		"another host":  func() config.AppLoginGrant { g := kiloGrant(path); g.Host = "evil.example"; return g }(),
	} {
		cfg := config.Default()
		cfg.VendorUsage.Grants = map[string]config.AppLoginGrant{"kilo": g}
		if got := grantedCredentials(cfg, accountsapi.Readers(), env); len(got) != 0 {
			t.Errorf("%s: read %+v", name, got)
		}
	}
}

// failingReader fails every reading with a plain error.
type failingReader struct{ namedReader }

func (failingReader) Read(context.Context, string) (accounts.Reading, error) {
	return accounts.Reading{}, errors.New("accounts: GET app.kilo.ai/api/trpc: status 500")
}

// Neither a failed read of the sign-in nor a failed reading puts the token
// in the daemon's log.
func TestAppLoginTokenNeverReachesTheLog(t *testing.T) {
	env := sealedEnv(t)
	path := writeKiloLogin(t, env.Home, appToken)
	cfg := config.Default()
	cfg.VendorUsage.Grants = map[string]config.AppLoginGrant{"kilo": kiloGrant(path)}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	readers := []accounts.Reader{failingReader{namedReader{"kilo"}}}
	creds := grantedCredentials(cfg, accountsapi.Readers(), env)
	p := accounts.NewPoller(nil, accounts.PollerOptions{
		Credentials: func() []accounts.Credential { return creds },
		Readers:     readers, Logger: logger,
	})
	p.Scan(context.Background())
	// A sign-in file that is no longer JSON fails without quoting it.
	if err := os.WriteFile(path, []byte(`{"kilo":{"access":"`+appToken+`"`), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Scan(context.Background())
	if logs.Len() == 0 || strings.Contains(logs.String(), appToken) {
		t.Errorf("log:\n%s", logs.String())
	}
}
