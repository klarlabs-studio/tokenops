package accounts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/infra/applogin"
)

// antigravityServer stands in for the app's language server: HTTPS on
// 127.0.0.1 with a self-signed certificate, refusing a wrong CSRF token.
func antigravityLS(t *testing.T, summary, status string) (port int) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Codeium-Csrf-Token") != "token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case agService + "RetrieveUserQuotaSummary":
			if summary == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(summary))
		case agService + "GetUserStatus":
			_, _ = w.Write([]byte(status))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ = strconv.Atoi(u.Port())
	return port
}

// antigravityReader reads the language server on port as process 77; the
// grant's token is the process's CSRF token and ID, as applogin reads them.
func antigravityReader(port int) Antigravity {
	return Antigravity{
		Ports: func(_ context.Context, pid int) ([]int, error) {
			if pid != 77 {
				return nil, nil
			}
			return []int{port}, nil
		},
	}
}

func agToken(csrf string) string { return `{"--csrf_token":"` + csrf + `","pid":"77"}` }

const agApp = "/Applications/Antigravity.app/Contents/Resources/bin/language_server_macos_arm --csrf_token token --app_data_dir antigravity --extension_server_port 54977"

// The grant's process sign-in, read from the app's command line by
// applogin exactly as the descriptor names it, is what the reader takes;
// other language servers and helpers are not matched.
func TestAntigravityGrantFindsTheAppsServer(t *testing.T) {
	d, _ := providers.Lookup("antigravity")
	s, ok := d.AppLoginSource()
	if !ok || s.Credential != providers.AppLogin {
		t.Fatal("Antigravity is read only by grant")
	}
	env := applogin.Env{Processes: func(context.Context) ([]applogin.Process, error) {
		return []applogin.Process{
			{PID: 5, Args: strings.Fields("/Applications/Antigravity.app/Contents/Frameworks/Antigravity Helper.app/Contents/MacOS/Antigravity Helper --type=renderer")},
			{PID: 6, Args: strings.Fields("/Applications/Cursor.app/bin/language_server --csrf_token cursor-token")},
			{PID: 77, Args: strings.Fields(agApp)},
		}, nil
	}}
	l, err := applogin.Locate(context.Background(), s.AppLogins, env)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := applogin.Read(context.Background(), l, env)
	if err != nil {
		t.Fatal(err)
	}
	port := antigravityLS(t, fixture(t, "antigravity"), "")
	if got, err := antigravityReader(port).ReadAppLogin(context.Background(), tok); err != nil || len(got.Windows) != 4 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAntigravityReadsTheQuotaSummary(t *testing.T) {
	port := antigravityLS(t, fixture(t, "antigravity"), "")
	got, err := antigravityReader(port).ReadAppLogin(context.Background(), agToken("token"))
	if err != nil || len(got.Windows) != 4 {
		t.Fatalf("got %+v, %v", got, err)
	}
	want := []struct {
		name string
		used float64
		d    time.Duration
	}{{"5h (Gemini)", 9, 5 * time.Hour}, {"5h (Claude/GPT)", 27, 5 * time.Hour}, {"week (Gemini)", 18, 7 * 24 * time.Hour}, {"week (Claude/GPT)", 36, 7 * 24 * time.Hour}}
	for i, w := range want {
		if g := got.Windows[i]; g.Name != w.name || !approx(g.UsedPct, w.used) || g.Duration != w.d || g.ResetsAt.IsZero() {
			t.Errorf("window %d = %+v, want %+v", i, g, w)
		}
	}
}

// An older app answers only GetUserStatus: the most used model per pool.
func TestAntigravityFallsBackToUserStatus(t *testing.T) {
	status := `{"code":0,"userStatus":{"email":"test@example.com","planStatus":{"planInfo":{"planName":"Pro"}},
  "cascadeModelConfigData":{"clientModelConfigs":[
    {"label":"Claude 3.5 Sonnet","modelOrAlias":{"model":"claude-3-5-sonnet"},"quotaInfo":{"remainingFraction":0.5,"resetTime":"2025-12-24T10:00:00Z"}},
    {"label":"Gemini Pro Low","modelOrAlias":{"model":"gemini-pro-low"},"quotaInfo":{"remainingFraction":0.8,"resetTime":"2025-12-24T11:00:00Z"}},
    {"label":"Gemini Flash","modelOrAlias":{"model":"gemini-flash"},"quotaInfo":{"remainingFraction":0.2,"resetTime":"2025-12-24T12:00:00Z"}}]}}}`
	port := antigravityLS(t, "", status)
	got, err := antigravityReader(port).ReadAppLogin(context.Background(), agToken("token"))
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "quota (Claude/GPT)" || !approx(w.UsedPct, 50) {
		t.Errorf("claude %+v", w)
	}
	if w := got.Windows[1]; w.Name != "quota (Gemini)" || !approx(w.UsedPct, 80) {
		t.Errorf("gemini %+v", w)
	}
}

// A token that is not the grant's shape is refused before any call.
func TestAntigravityRefusesWhatIsNotItsToken(t *testing.T) {
	for _, tok := range []string{"", "token", `{"pid":"77"}`, `{"--csrf_token":"token"}`, `{"--csrf_token":"token","pid":"x"}`} {
		if _, err := antigravityReader(1).Read(context.Background(), tok); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q: %v", tok, err)
		}
	}
}

func TestAntigravityRefusedTokenAndShapes(t *testing.T) {
	port := antigravityLS(t, fixture(t, "antigravity"), "")
	if _, err := antigravityReader(port).ReadAppLogin(context.Background(), agToken("other")); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("wrong token: %v", err)
	}
	oneof := `{"groups":[{"displayName":"Gemini Models","buckets":[{"bucketId":"gemini-weekly","displayName":"Weekly Limit","remaining":{"case":"remainingFraction","value":0.5}}]}]}`
	if got, ok := parseAntigravitySummary([]byte(oneof)); !ok || got.Windows[0].Name != "week (Gemini)" || got.Windows[0].UsedPct != 50 {
		t.Errorf("oneof %+v %v", got, ok)
	}
	unknown := `{"response":{"groups":[{"displayName":"Gemini Models","buckets":[
    {"bucketId":"gemini-weekly","displayName":"Weekly Limit","description":"Refreshes later."},
    {"bucketId":"gemini-5h","displayName":"Five Hour Limit","disabled":true,"remaining":{"remainingFraction":0.5}}]}]}}`
	if _, ok := parseAntigravitySummary([]byte(unknown)); ok {
		t.Error("buckets without a measured fraction read as usage")
	}
}

// The loopback client dials nothing but 127.0.0.1.
func TestLoopbackClientStaysOnTheMachine(t *testing.T) {
	resp, err := loopbackClient().Get("https://example.com/")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("the loopback client reached another host")
	}
}
