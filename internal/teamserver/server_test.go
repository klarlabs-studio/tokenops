package teamserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/internal/contexts/team"
	"go.klarlabs.de/tokenops/internal/teamserver"
	"go.klarlabs.de/tokenops/internal/teamserver/pgstore"
	"go.klarlabs.de/tokenops/internal/teamserver/pgstore/pgtest"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

type env struct {
	t     *testing.T
	srv   *httptest.Server
	store *pgstore.Store
	admin string
	login string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := pgtest.New(t)
	_, _, admin, login, err := store.CreateOrg(context.Background(), "Acme", "Olivia Owner")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, store: store, admin: admin, login: login}
	var handler http.Handler
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(e.srv.Close)
	s, err := teamserver.New(store, teamserver.Config{PublicURL: e.srv.URL, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	handler = s.Handler()
	return e
}

func (e *env) do(method, path, token string, body any) (int, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (e *env) join(teamName, name string, role team.Role) teamwire.EnrollResponse {
	e.t.Helper()
	e.do("POST", "/api/v1/teams", e.admin, map[string]string{"name": teamName})
	code, body := e.do("POST", "/api/v1/invites", e.admin, map[string]any{"team": teamName, "role": role})
	if code != http.StatusCreated {
		e.t.Fatalf("invite: %d %s", code, body)
	}
	var inv teamserver.InviteAnswer
	_ = json.Unmarshal(body, &inv)
	code, body = e.do("POST", "/api/v1/enroll", "", teamwire.EnrollRequest{Invite: inv.Invite, DisplayName: name, DeviceName: "laptop"})
	if code != http.StatusCreated {
		e.t.Fatalf("enroll: %d %s", code, body)
	}
	var r teamwire.EnrollResponse
	_ = json.Unmarshal(body, &r)
	return r
}

func today() string { return time.Now().UTC().Format(teamwire.DayLayout) }

func upload(buckets ...teamwire.Bucket) teamwire.Upload {
	return teamwire.Upload{Schema: 1, BatchID: uuid.NewString(), ComputedAt: time.Now().UTC(), ClientVersion: "test",
		Days: []string{today()}, Buckets: buckets}
}

func TestEndToEnd(t *testing.T) {
	e := newEnv(t)
	var members []teamwire.EnrollResponse
	for _, name := range []string{"Ann", "Ben", "Cem"} {
		m := e.join("platform", name, team.RoleMember)
		members = append(members, m)
		code, body := e.do("POST", "/api/v1/ingest", m.DeviceToken, upload(
			teamwire.Bucket{Day: today(), Repo: "acme/api", Kind: "edit", Instructions: 2, FirstTry: 1, Tokens: 100, APIEquivalentUSD: 1}))
		if code != http.StatusOK {
			t.Fatalf("ingest: %d %s", code, body)
		}
	}
	lead := e.join("platform", "Lars", team.RoleLead)

	// Aggregates: three people, shown; any member may read them.
	code, body := e.do("GET", "/api/v1/aggregates?by=team&period=day", members[0].DeviceToken, nil)
	if code != http.StatusOK {
		t.Fatalf("aggregates: %d %s", code, body)
	}
	var agg teamserver.AggregateAnswer
	_ = json.Unmarshal(body, &agg)
	if len(agg.Rows) != 1 || agg.Rows[0].Suppressed || agg.Rows[0].People != 3 || agg.Rows[0].Totals.Tokens != 300 {
		t.Errorf("aggregate = %s", body)
	}
	// A single repository with one contributor is withheld.
	solo := e.join("data", "Dee", team.RoleMember)
	e.do("POST", "/api/v1/ingest", solo.DeviceToken, upload(teamwire.Bucket{Day: today(), Repo: "acme/etl", Kind: "deep", Instructions: 9}))
	_, body = e.do("GET", "/api/v1/aggregates?by=repo&period=week", e.admin, nil)
	_ = json.Unmarshal(body, &agg)
	for _, r := range agg.Rows {
		if r.Group == "acme/etl" && (!r.Suppressed || r.Totals.Instructions != 0) {
			t.Errorf("one person's repo was shown: %+v", r)
		}
	}

	// Drill-down: refused without a grant, even for the owner.
	path := "/api/v1/members/" + members[0].MemberID + "/metrics"
	if code, _ := e.do("GET", path, e.admin, nil); code != http.StatusForbidden {
		t.Errorf("owner without grant: %d", code)
	}
	if code, _ := e.do("GET", path, lead.DeviceToken, nil); code != http.StatusForbidden {
		t.Errorf("lead without grant: %d", code)
	}
	if code, _ := e.do("GET", path, members[0].DeviceToken, nil); code != http.StatusOK {
		t.Errorf("self: %d", code)
	}
	if code, body := e.do("POST", "/api/v1/grants", e.admin, map[string]string{"grantee_id": lead.MemberID, "team": "platform", "reason": "1:1s"}); code != http.StatusCreated {
		t.Fatalf("grant: %d %s", code, body)
	}
	code, body = e.do("GET", path, lead.DeviceToken, nil)
	var ma teamserver.MemberAnswer
	_ = json.Unmarshal(body, &ma)
	if code != http.StatusOK || !ma.Recorded || ma.Member != "Ann" {
		t.Errorf("granted view: %d %s", code, body)
	}
	// The member is told: the grant and the view are on their page.
	_, body = e.do("GET", "/api/v1/me", members[0].DeviceToken, nil)
	var me teamwire.Me
	_ = json.Unmarshal(body, &me)
	if len(me.Viewers) != 1 || me.Viewers[0].Name != "Lars" || len(me.Views) != 1 || me.Views[0].Viewer != "Lars" {
		t.Errorf("me = %s", body)
	}
	// A device cannot administer.
	if code, _ := e.do("POST", "/api/v1/teams", lead.DeviceToken, map[string]string{"name": "x"}); code != http.StatusForbidden {
		t.Errorf("device created a team: %d", code)
	}
	// The audit log records the view.
	_, body = e.do("GET", "/api/v1/audit", e.admin, nil)
	if !strings.Contains(string(body), `"member.viewed"`) {
		t.Errorf("audit lacks the view: %s", body)
	}
	// Leaving erases by default and revokes the token.
	if code, _ := e.do("DELETE", "/api/v1/devices/self", members[0].DeviceToken, nil); code != http.StatusOK {
		t.Errorf("leave: %d", code)
	}
	if code, _ := e.do("GET", "/api/v1/me", members[0].DeviceToken, nil); code != http.StatusUnauthorized {
		t.Errorf("revoked token still works: %d", code)
	}
}

func TestIngestRefusesFreeTextAndBadInput(t *testing.T) {
	e := newEnv(t)
	m := e.join("platform", "Ann", team.RoleMember)
	cases := map[string]any{
		"path as repo":   upload(teamwire.Bucket{Day: today(), Repo: "/Users/ann/secret", Kind: "edit"}),
		"prompt as kind": upload(teamwire.Bucket{Day: today(), Repo: "acme/api", Kind: "fix the login bug"}),
		// An extra field, such as a prompt, is refused before validation.
		"unknown field": map[string]any{"schema": 1, "batch_id": uuid.NewString(), "computed_at": time.Now(),
			"days": []string{today()}, "buckets": []any{}, "prompt": "fix the login bug"},
	}
	for name, body := range cases {
		code, out := e.do("POST", "/api/v1/ingest", m.DeviceToken, body)
		if code != http.StatusUnprocessableEntity && code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, code, out)
		}
	}
	if code, _ := e.do("POST", "/api/v1/ingest", "", upload()); code != http.StatusUnauthorized {
		t.Errorf("anonymous upload: %d", code)
	}
	if code, _ := e.do("POST", "/api/v1/ingest", e.admin, upload()); code != http.StatusUnauthorized {
		t.Errorf("admin token uploaded: %d", code)
	}
	huge := bytes.Repeat([]byte("a"), teamwire.MaxUploadBytes+10)
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/ingest", bytes.NewReader(huge))
	req.Header.Set("Authorization", "Bearer "+m.DeviceToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversized upload: %d", resp.StatusCode)
	}
}

func TestIngestIsRateLimited(t *testing.T) {
	e := newEnv(t)
	m := e.join("platform", "Ann", team.RoleMember)
	limited := false
	for range 10 {
		if code, _ := e.do("POST", "/api/v1/ingest", m.DeviceToken, upload()); code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("ten uploads in a row were all accepted")
	}
}

func TestWebSignIn(t *testing.T) {
	e := newEnv(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.Get(e.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous overview: %d", resp.StatusCode)
	}
	// Opening the link does not use it up; posting the form does.
	resp, _ = client.Get(e.srv.URL + "/login?t=" + e.login)
	_ = resp.Body.Close()
	resp, err = client.PostForm(e.srv.URL+"/login", url.Values{"t": {e.login}})
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(page), "Overview") {
		t.Fatalf("signed-in overview: %d", resp.StatusCode)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("csp = %q", csp)
	}
	for _, p := range []string{"/me", "/members", "/audit", "/?by=kind&period=day"} {
		resp, err := client.Get(e.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	// The link is single-use.
	resp, _ = http.PostForm(e.srv.URL+"/login", url.Values{"t": {e.login}})
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("second use of the link: %d", resp.StatusCode)
	}
	// A cross-site form post is refused.
	req, _ := http.NewRequest("POST", e.srv.URL+"/logout", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, _ = client.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site logout: %d", resp.StatusCode)
	}
}
