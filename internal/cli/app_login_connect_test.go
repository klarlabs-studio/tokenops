package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/connections"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/providersetup"
	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/applogin"
	"go.klarlabs.de/tokenops/internal/proxy"
)

// passAPI stands in for the API token check, which this test is past.
type passAPI struct{}

func (passAPI) Middleware(next http.Handler) http.Handler { return next }

// The CLI's `setup cursor --use-app-login` and the menu bar's Allow (the
// daemon's POST /api/connections/connect) run one grant flow: from the same
// sign-in and the same vendor answer they record the same grant, and the
// consent the daemon serves is the one the CLI prints.
func TestCLIAndAPIRecordTheSameGrant(t *testing.T) {
	home := t.TempDir()
	// Nothing reaches the operator's own home or config (config is XDG).
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(k, filepath.Join(home, "env", k))
	}
	t.Setenv("TOKENOPS_KEYCHAIN_DISABLED", "1")
	db := filepath.Join(home, "Library/Application Support/Cursor/User/globalStorage/state.vscdb")
	if err := os.MkdirAll(filepath.Dir(db), 0o700); err != nil {
		t.Fatal(err)
	}
	writeCursorStateDB(t, db, "eyJ-CURSOR-SHARED-SECRET")
	env := applogin.Env{Home: home, Getenv: func(string) string { return "" }}
	verify := func(context.Context, string, providersetup.AppLogin, applogin.Env) ([]string, error) {
		return []string{"weekly pool: 37% used"}, nil
	}
	prevEnv, prevVerify := appLoginEnv, verifyAppLogin
	appLoginEnv = func(disabled bool) applogin.Env { e := env; e.KeychainDisabled = disabled; return e }
	verifyAppLogin = verify
	t.Cleanup(func() { appLoginEnv, verifyAppLogin = prevEnv, prevVerify })

	cliPath := seedConfig(t)
	out, err := runCookieSetupCmd(t, "y\n", "cursor", "--use-app-login", "--config", cliPath, "--no-restart")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}

	apiPath := seedConfig(t)
	srv := proxy.New("127.0.0.1:0",
		proxy.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		proxy.WithShutdownTimeout(time.Second),
		proxy.WithDashAuth(passAPI{}),
		proxy.WithPlans(func() headroom.Deps { return headroom.Deps{} }),
		proxy.WithConnections(func(headroom.Glance, []state.SourceReport, time.Time) []connections.Connection { return nil }),
		proxy.WithActions(func() proxy.ActionDeps {
			return proxy.ActionDeps{ConfigPath: apiPath, AppLogin: providersetup.GrantDeps{Env: env, Verify: verify}}
		}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		sctx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(sctx)
	})
	base := "http://" + srv.Addr()
	resp, err := http.Get(base + "/api/connections/connect?provider=cursor")
	if err != nil {
		t.Fatal(err)
	}
	var consent providersetup.Consent
	err = json.NewDecoder(resp.Body).Decode(&consent)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("consent = %d %v", resp.StatusCode, err)
	}
	for _, line := range []string{"  What is read:   " + consent.What, "  Sent only to:   " + consent.Host, "  Stored:         " + consent.Stored} {
		if !bytes.Contains([]byte(out), []byte(line)) {
			t.Errorf("the CLI did not print the daemon's consent line %q:\n%s", line, out)
		}
	}
	body, _ := json.Marshal(proxy.ConnectConnectionRequest{Provider: "cursor", What: consent.What, Host: consent.Host})
	resp, err = http.Post(base+"/api/connections/connect", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("connect = %d %s", resp.StatusCode, answer)
	}

	grant := func(path string) config.AppLoginGrant {
		cfg, err := config.ReadMutable(path)
		if err != nil {
			t.Fatal(err)
		}
		g, ok := cfg.VendorUsage.Grants["cursor"]
		if !ok {
			t.Fatalf("no grant in %s", path)
		}
		g.GrantedAt = time.Time{}
		return g
	}
	if c, a := grant(cliPath), grant(apiPath); !reflect.DeepEqual(c, a) {
		t.Errorf("the CLI granted %+v, the API %+v", c, a)
	}
}
