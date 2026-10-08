package providersetup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/infra/applogin"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// appLoginReader records how it was read.
type appLoginReader struct{ viaAppLogin, viaKey []string }

func (*appLoginReader) Endpoint() string               { return "clinepass" }
func (*appLoginReader) Provider() eventschema.Provider { return "clinepass" }
func (*appLoginReader) Source() string                 { return "clinepass-account" }
func (r *appLoginReader) Read(_ context.Context, key string) (usage.Reading, error) {
	r.viaKey = append(r.viaKey, key)
	return usage.Reading{HasBalance: true, BalanceUSD: 1}, nil
}

func (r *appLoginReader) ReadAppLogin(_ context.Context, token string) (usage.Reading, error) {
	r.viaAppLogin = append(r.viaAppLogin, token)
	return usage.Reading{HasBalance: true, BalanceUSD: 2}, nil
}

// Finding a sign-in reads nothing secret; verifying reads it once and hands
// it to the reader as an app login; the grant records what was shown.
func TestAppLoginFindVerifyGrant(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".cline/data/settings/providers.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"providers":{"cline":{"settings":{"auth":{"accessToken":"at-1"}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := applogin.Env{Home: home, Getenv: func(string) string { return "" }}
	a, err := FindAppLogin(context.Background(), "clinepass", env)
	if err != nil {
		t.Fatal(err)
	}
	if a.Host != "api.cline.bot" || !strings.Contains(a.What, path) || !strings.Contains(a.What, "accessToken") || strings.Contains(a.What, "at-1") {
		t.Errorf("%+v", a)
	}
	r := &appLoginReader{}
	lines, err := VerifyAppLoginWith(context.Background(), []usage.Reader{r}, "clinepass", a, env)
	if err != nil || len(r.viaAppLogin) != 1 || r.viaAppLogin[0] != "at-1" || len(r.viaKey) != 0 || !strings.Contains(strings.Join(lines, " "), "2.00") {
		t.Fatalf("%v %v %+v", lines, err, r)
	}
	var cfg config.Config
	ApplyGrant(&cfg, "clinepass", a, time.Unix(0, 0))
	g := cfg.VendorUsage.Grants["clinepass"]
	if g.Item != path || g.Host != "api.cline.bot" || g.Kind != "json-file" {
		t.Errorf("%+v", g)
	}
	if rows := Grants(cfg, env); len(rows) != 1 || !rows[0].Current {
		t.Errorf("%+v", rows)
	}
	if !RevokeGrant(&cfg, "clinepass") || RevokeGrant(&cfg, "clinepass") || cfg.VendorUsage.Grants != nil {
		t.Errorf("revoke: %+v", cfg.VendorUsage.Grants)
	}
}

func TestAppLoginNotFound(t *testing.T) {
	env := applogin.Env{Home: t.TempDir(), Getenv: func(string) string { return "" }}
	if _, err := FindAppLogin(context.Background(), "kilo", env); !errors.Is(err, applogin.ErrNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := FindAppLogin(context.Background(), "openrouter", env); !errors.Is(err, ErrNoAppLogin) {
		t.Errorf("%v", err)
	}
}
