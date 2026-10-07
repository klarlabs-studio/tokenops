package claudeusagemeter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
)

// Client.Organizations sends the sessionKey cookie + browser UA and
// decodes the array shape.
func TestClientOrganizationsHappyPath(t *testing.T) {
	var gotCookie, gotUA, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleOrgs))
	}))
	defer srv.Close()
	c := NewClient("sk")
	c.BaseURL = srv.URL
	orgs, err := c.Organizations(context.Background())
	if err != nil {
		t.Fatalf("Organizations: %v", err)
	}
	if gotCookie != "sessionKey=sk" {
		t.Errorf("cookie = %q", gotCookie)
	}
	if gotUA == "" {
		t.Errorf("user-agent must be set to avoid Cloudflare 403")
	}
	if gotPath != "/api/organizations" {
		t.Errorf("path = %q", gotPath)
	}
	if len(orgs) != 1 || orgs[0].UUID != "org-abc" {
		t.Errorf("orgs = %+v", orgs)
	}
}

// Empty cookie short-circuits with ErrMissingCookie before HTTP.
func TestClientMissingCookie(t *testing.T) {
	if _, err := (&Client{}).Organizations(context.Background()); err != usage.ErrMissingCookie {
		t.Errorf("want ErrMissingCookie; got %v", err)
	}
	if _, err := (&Client{}).Usage(context.Background(), "x"); err != usage.ErrMissingCookie {
		t.Errorf("want ErrMissingCookie; got %v", err)
	}
}

// 401 maps to ErrUnauthorized so the poller can log the specific
// "re-paste cookie" hint instead of generic http noise.
func TestClient401MapsToErrUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	c := NewClient("expired")
	c.BaseURL = srv.URL
	if _, err := c.Organizations(context.Background()); err != usage.ErrUnauthorized {
		t.Errorf("want ErrUnauthorized; got %v", err)
	}
}

// claude.ai answers an expired session with a 403 permission_error, not a
// 401 (observed 2026-10-06). It is the same remedy, so it is the same
// error, and the poller refreshes the session from the browser for it.
// Any other 403 stays a plain error.
func TestClientExpiredSession403MapsToErrUnauthorized(t *testing.T) {
	for body, want := range map[string]bool{
		`{"type":"error","error":{"type":"permission_error","message":"Invalid authorization","details":{"error_code":"account_session_invalid"}}}`: true,
		`{"type":"error","error":{"type":"permission_error","message":"not a member of this organization"}}`:                                        false,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(body))
		}))
		c := NewClient("expired")
		c.BaseURL = srv.URL
		_, err := c.Usage(context.Background(), "org")
		srv.Close()
		if got := errors.Is(err, usage.ErrUnauthorized); got != want {
			t.Errorf("%s: ErrUnauthorized = %v, want %v (err %v)", body, got, want, err)
		}
		if want && !(errors.Is(err, usage.ErrBotCheck) || errors.Is(err, usage.ErrUnauthorized)) {
			t.Error("an expired session does not trigger a refresh from the browser")
		}
	}
}

// claude.ai sits behind a bot check that answered 403 "Just a moment..." to
// the session cookie alone — which is why this meter never worked. The
// browser's clearance cookie and its own User-Agent are what passed the
// check, and they have to travel together.
func TestClientSendsTheClearanceCookieAndBrowserAgent(t *testing.T) {
	var gotCookie, gotUA, gotPlatform, gotAuthorization string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie, gotUA = r.Header.Get("Cookie"), r.Header.Get("User-Agent")
		gotPlatform = r.Header.Get("Sec-Ch-Ua-Platform")
		gotAuthorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := NewClient("sk-ant-sid-x")
	c.BaseURL, c.Clearance, c.UserAgent = srv.URL, "clearance-token", "Mozilla/5.0 Chrome/141.0.0.0"
	c.BrowserHeaders = map[string]string{
		"sec-ch-ua-platform": `"macOS"`,
		"authorization":      "must-not-be-sent",
	}
	c.BrowserCookies = map[string]string{
		"__cf_bm":       "bot-secret",
		"unsafe-cookie": "must-not-be-sent",
	}
	if _, err := c.Organizations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotCookie, "sessionKey=sk-ant-sid-x") || !strings.Contains(gotCookie, "cf_clearance=clearance-token") {
		t.Errorf("cookie header = %q", strings.ReplaceAll(gotCookie, "sk-ant-sid-x", "<key>"))
	}
	if gotUA != "Mozilla/5.0 Chrome/141.0.0.0" {
		t.Errorf("user agent = %q, want the browser's own", gotUA)
	}
	if gotPlatform != `"macOS"` {
		t.Errorf("client hint = %q, want the imported browser value", gotPlatform)
	}
	if gotAuthorization != "" {
		t.Errorf("unsafe configured header was sent: %q", gotAuthorization)
	}
	if !strings.Contains(gotCookie, "__cf_bm=bot-secret") || strings.Contains(gotCookie, "unsafe-cookie") {
		t.Errorf("browser cookie allowlist not enforced: %q", strings.ReplaceAll(gotCookie, "sk-ant-sid-x", "<key>"))
	}
}

// Without a clearance cookie the request still goes out, with a plain
// browser agent: the check is not always asking.
func TestClientWithoutClearanceStillSendsTheSession(t *testing.T) {
	var gotCookie, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie, gotUA = r.Header.Get("Cookie"), r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := NewClient("sk-ant-sid-y")
	c.BaseURL = srv.URL
	if _, err := c.Organizations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotCookie, "cf_clearance") || gotUA == "" {
		t.Errorf("cookie = %q agent = %q", strings.ReplaceAll(gotCookie, "sk-ant-sid-y", "<key>"), gotUA)
	}
}
