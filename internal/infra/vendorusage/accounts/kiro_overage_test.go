package accounts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/infra/applogin"
)

const kiroARN = "arn:aws:codewhisperer:us-east-1:123456789012:profile/ABCDEF"

func kiroToken(access, arn string) string {
	b, _ := json.Marshal(map[string]string{"access_token": access, "profile_arn": arn})
	return string(b)
}

func TestKiroOverageReadsPlanAndOverage(t *testing.T) {
	srv := headerServer(t, http.MethodPost, "/", map[string]string{
		"Authorization": "Bearer kt", "X-Amz-Target": "AmazonCodeWhispererService.GetUsageLimits",
		"Content-Type": "application/x-amz-json-1.0",
	}, fixture(t, "kiro-overage"))
	got, err := KiroOverage{BaseURL: srv.URL}.ReadAppLogin(context.Background(), kiroToken("kt", kiroARN))
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	reset := time.Unix(1788220800, 0).UTC()
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 100) || !w.ResetsAt.Equal(reset) {
		t.Errorf("plan %+v", w)
	}
	if w := got.Windows[1]; w.Name != "overage" || !approx(w.UsedPct, 36.0349) {
		t.Errorf("overage %+v", w)
	}
	for _, tok := range []string{"", "kt", kiroToken("", kiroARN)} {
		if _, err := (KiroOverage{BaseURL: srv.URL}).Read(context.Background(), tok); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q: %v", tok, err)
		}
	}
	for _, arn := range []string{"", "arn:aws:codewhisperer:ap-south-1:1:profile/x", "arn:aws:iam::1:role/x", "arn:aws:codewhisperer:us-east-1:1:profile/"} {
		if _, err := (KiroOverage{BaseURL: srv.URL}).Read(context.Background(), kiroToken("kt", arn)); err == nil {
			t.Errorf("%q accepted", arn)
		}
	}
}

// The grant reads exactly the access token and the profile ARN out of
// kiro-cli's database, through the descriptor's own query, from a copy.
func TestKiroGrantReadsTheDatabase(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".local/share/kiro-cli/data.sqlite3")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE auth_kv (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE state (key TEXT PRIMARY KEY, value TEXT)`,
		`INSERT INTO auth_kv VALUES ('kirocli:odic:token', '{"access_token":"kt","refresh_token":"never-read","expires_at":"2030-01-01T00:00:00Z"}')`,
		`INSERT INTO state VALUES ('api.codewhisperer.profile', '{"arn":"` + kiroARN + `","profile_name":"default"}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	d, _ := providers.Lookup("kiro")
	s, ok := d.AppLoginSource()
	if !ok || s.Tag != "kiro-overage" {
		t.Fatalf("%+v", s)
	}
	env := applogin.Env{Home: home, Getenv: func(string) string { return "" }}
	l, err := applogin.Locate(context.Background(), s.AppLogins, env)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := applogin.Read(context.Background(), l, env)
	if err != nil || tok != kiroToken("kt", kiroARN) {
		t.Fatalf("%q %v", tok, err)
	}
}
