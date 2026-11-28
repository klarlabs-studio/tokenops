package providersetup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/applogin"
	"go.klarlabs.de/tokenops/internal/infra/keychain"
)

const cursorSecret = "eyJ-CURSOR-GRANT-SECRET"

// cursorHome is a temporary home with Cursor's state database holding its
// access token, and the environment that finds it there. The Keychain is
// never read: Cursor's token is stored plain here, so no item opens it.
func cursorHome(t *testing.T) (string, applogin.Env) {
	t.Helper()
	home := t.TempDir()
	// Nothing reaches the operator's own home or config (config is XDG).
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(k, filepath.Join(home, "env", k))
	}
	path := filepath.Join(home, "Library/Application Support/Cursor/User/globalStorage/state.vscdb")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, q := range []string{
		`CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`,
		`INSERT INTO ItemTable VALUES ('cursorAuth/accessToken', '` + cursorSecret + `')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	env := applogin.Env{Home: home, Getenv: func(string) string { return "" },
		Keychain: func(i keychain.Item) (string, error) {
			t.Errorf("read the Keychain item %s quietly", i)
			return "", keychain.ErrNotFound
		},
		Prompt: func(_ context.Context, i keychain.Item) (string, error) {
			t.Errorf("asked macOS for the Keychain item %s", i)
			return "", keychain.ErrDenied
		}}
	return path, env
}

// seeded is a config file with one setting and no grant.
func seeded(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("log:\n  level: info\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The consent is what the CLI prints before y/N: the item and its field,
// the one host, the Keychain item macOS asks about, and that nothing
// secret is stored.
func TestConsentSaysWhatIsRead(t *testing.T) {
	db, env := cursorHome(t)
	a, err := FindAppLogin(context.Background(), "cursor", env)
	if err != nil {
		t.Fatal(err)
	}
	c := a.Consent("cursor")
	if c.Provider != "cursor" || c.Name != "Cursor" || c.App != "Cursor" || c.Host != "cursor.com" ||
		c.Keychain != "Cursor Safe Storage" || c.Stored != StoredNothingSecret {
		t.Errorf("consent = %+v", c)
	}
	for _, want := range []string{db, "column value", "cursorAuth/accessToken", `"Cursor Safe Storage"`} {
		if !strings.Contains(c.What, want) {
			t.Errorf("what = %q, lacks %q", c.What, want)
		}
	}
	if strings.Contains(fmt.Sprint(c), cursorSecret) {
		t.Error("the consent carries the token")
	}
}

// The grant is recorded only when the vendor accepted the sign-in; a
// refusal leaves the config as it was.
func TestGrantAppLoginWritesOnlyOnTheVendorsYes(t *testing.T) {
	_, env := cursorHome(t)
	a, err := FindAppLogin(context.Background(), "cursor", env)
	if err != nil {
		t.Fatal(err)
	}
	path := seeded(t)
	before, _ := os.ReadFile(path)
	refuse := func(context.Context, string, AppLogin, applogin.Env) ([]string, error) {
		return nil, fmt.Errorf("%w (401)", ErrRefused)
	}
	_, err = GrantAppLogin(context.Background(), path, "cursor", a, GrantDeps{Env: env, Verify: refuse})
	var na *NotAcceptedError
	if !errors.As(err, &na) || !errors.Is(err, ErrRefused) {
		t.Fatalf("refused = %v, want NotAcceptedError wrapping ErrRefused", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("a refusal wrote the config:\n%s", after)
	}

	at := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	accept := func(context.Context, string, AppLogin, applogin.Env) ([]string, error) {
		return []string{"weekly pool: 37% used"}, nil
	}
	lines, err := GrantAppLogin(context.Background(), path, "cursor", a, GrantDeps{Env: env, Verify: accept, Now: func() time.Time { return at }})
	if err != nil || len(lines) != 1 {
		t.Fatalf("grant = %v %v", lines, err)
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		t.Fatal(err)
	}
	if g := cfg.VendorUsage.Grants["cursor"]; g.Host != "cursor.com" || g.UnlockService != "Cursor Safe Storage" || !g.GrantedAt.Equal(at) {
		t.Errorf("grant = %+v", g)
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), cursorSecret) {
		t.Error("the config holds the token")
	}
}

// Connecting from a surface runs the same grant, only for exactly what the
// operator was shown, and says a failure in plain words: nothing is
// written, and no answer carries the token.
func TestConnectAppLogin(t *testing.T) {
	_, env := cursorHome(t)
	a, err := FindAppLogin(context.Background(), "cursor", env)
	if err != nil {
		t.Fatal(err)
	}
	shown := Shown{What: a.What, Host: a.Host}
	ok := func(context.Context, string, AppLogin, applogin.Env) ([]string, error) {
		return []string{"weekly pool: 37% used"}, nil
	}
	fail := func(err error) VerifyFunc {
		return func(context.Context, string, AppLogin, applogin.Env) ([]string, error) { return nil, err }
	}
	tests := []struct {
		name    string
		id      string
		shown   Shown
		env     applogin.Env
		verify  VerifyFunc
		failure ConnectFailure
		says    string
	}{
		{name: "not shown", id: "cursor", env: env, verify: ok, failure: ConnectNotShown, says: "show"},
		{name: "changed", id: "cursor", shown: Shown{What: "the file /elsewhere", Host: a.Host}, env: env, verify: ok,
			failure: ConnectChanged, says: "changed since you were shown it"},
		{name: "not an app login", id: "openrouter", shown: shown, env: env, verify: ok, failure: ConnectNotAppLogin, says: "OpenRouter"},
		{name: "no sign-in", id: "cursor", shown: shown, env: applogin.Env{Home: t.TempDir(), Getenv: func(string) string { return "" }},
			verify: ok, failure: ConnectNoSignIn, says: "Sign in to Cursor, then try again"},
		{name: "expired", id: "cursor", shown: shown, env: env, verify: fail(fmt.Errorf("%w (401)", ErrRefused)),
			failure: ConnectRefused, says: "Sign in to Cursor, then try again"},
		{name: "keychain denied", id: "cursor", shown: shown, env: env, verify: fail(fmt.Errorf("applogin: Keychain item %q: %w", "Cursor Safe Storage", keychain.ErrDenied)),
			failure: ConnectKeychain, says: `macOS did not allow TokenOps to use "Cursor Safe Storage"`},
		{name: "vendor down", id: "cursor", shown: shown, env: env, verify: fail(errors.New("cursor.com /api/usage-summary: 503")),
			failure: ConnectUnreadable, says: "Couldn't read Cursor's plan"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := seeded(t)
			before, _ := os.ReadFile(path)
			_, err := ConnectAppLogin(context.Background(), path, tt.id, tt.shown, GrantDeps{Env: tt.env, Verify: tt.verify})
			var ce *ConnectError
			if !errors.As(err, &ce) || ce.Failure != tt.failure || !strings.Contains(ce.Error(), tt.says) {
				t.Fatalf("err = %v (%#v), want %s saying %q", err, ce, tt.failure, tt.says)
			}
			if strings.Contains(ce.Error(), cursorSecret) {
				t.Error("the error carries the token")
			}
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Errorf("wrote the config:\n%s", after)
			}
		})
	}

	path := seeded(t)
	got, err := ConnectAppLogin(context.Background(), path, "cursor", shown, GrantDeps{Env: env, Verify: ok})
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "cursor" || got.App != "Cursor" || got.Host != "cursor.com" || got.Config != path ||
		got.Summary != "Connected — reading Cursor's plan" || len(got.Reports) != 1 {
		t.Errorf("connected = %+v", got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), cursorSecret) {
		t.Error("the answer carries the token")
	}
	cfg, _ := config.ReadMutable(path)
	if _, ok := cfg.VendorUsage.Grants["cursor"]; !ok {
		t.Error("no grant recorded")
	}
}
