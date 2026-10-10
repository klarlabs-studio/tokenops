package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// selfServeStub is a team server with device links and the admin API.
type selfServeStub struct {
	mu       sync.Mutex
	polls    int
	approve  int // the poll on which the link is approved
	linkReq  teamwire.DeviceLinkRequest
	removed  string
	role     [2]string
	grant    teamwire.GrantRequest
	invite   teamwire.InviteRequest
	revoked  bool
	paused   bool
	ingested int
}

const (
	stubDeviceCode = "tot_dlc_secret"
	stubAdminToken = "tot_adm_secret"
	stubDevToken   = "tot_dev_secret"
)

func (st *selfServeStub) handler(t *testing.T, base func() string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		auth := r.Header.Get("Authorization")
		admin := auth == "Bearer "+stubAdminToken
		fail := func(status int, code, msg string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(teamwire.Error{Error: msg, Code: code})
		}
		route := r.Method + " " + r.URL.Path
		if strings.HasPrefix(r.URL.Path, "/api/v1/") && !admin && !strings.HasPrefix(route, "POST /api/v1/device-links") &&
			route != "GET /api/v1/me" && route != "POST /api/v1/ingest" {
			fail(http.StatusUnauthorized, "", "unauthorized")
			return
		}
		switch route {
		case "POST /api/v1/device-links":
			_ = json.NewDecoder(r.Body).Decode(&st.linkReq)
			_ = json.NewEncoder(w).Encode(teamwire.DeviceLink{DeviceCode: stubDeviceCode, UserCode: "BCDF-GHJK",
				VerificationURL: base() + "/link", ExpiresAt: time.Now().Add(10 * time.Minute), Interval: 5})
		case "POST /api/v1/device-links/token":
			var p teamwire.DeviceLinkPoll
			_ = json.NewDecoder(r.Body).Decode(&p)
			if p.DeviceCode != stubDeviceCode {
				fail(http.StatusBadRequest, teamwire.CodeExpiredToken, "unknown")
				return
			}
			st.polls++
			if st.polls < st.approve {
				fail(http.StatusBadRequest, teamwire.CodeAuthorizationPending, "pending")
				return
			}
			res := teamwire.DeviceLinkResult{}
			if st.linkReq.Enroll {
				res.Enrollment = &teamwire.EnrollResponse{OrgName: "Acme", TeamName: "core", MemberID: "m1", DeviceID: "d1", DeviceToken: stubDevToken}
			}
			if st.linkReq.Admin {
				res.Admin = &teamwire.AdminCredential{Token: stubAdminToken, ExpiresAt: time.Now().Add(30 * 24 * time.Hour), OrgName: "Acme", Role: teamwire.RoleOwner}
			}
			_ = json.NewEncoder(w).Encode(res)
		case "GET /api/v1/me":
			end := time.Now().Add(14 * 24 * time.Hour)
			first := time.Now().Add(9 * 24 * time.Hour)
			_ = json.NewEncoder(w).Encode(teamwire.Me{DisplayName: "Ada", Role: teamwire.RoleOwner, MinGroupSize: 3,
				Plan: &teamwire.Plan{Status: teamwire.PlanTrialing, TrialEndsAt: &end, FirstWeekAt: &first}})
		case "POST /api/v1/ingest":
			if st.paused {
				fail(http.StatusPaymentRequired, teamwire.CodeUploadsPaused, "the trial ended without a subscription")
				return
			}
			st.ingested++
			_ = json.NewEncoder(w).Encode(teamwire.IngestResponse{Accepted: 1})
		case "GET /api/v1/teams":
			_ = json.NewEncoder(w).Encode([]teamwire.Team{{ID: "t1", Name: "core", Members: 2}})
		case "POST /api/v1/teams":
			var req teamwire.CreateTeamRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(teamwire.Team{ID: "t2", Name: req.Name})
		case "POST /api/v1/invites":
			_ = json.NewDecoder(r.Body).Decode(&st.invite)
			_ = json.NewEncoder(w).Encode(teamwire.Invite{Invite: "tot_inv_x", ExpiresAt: time.Now().Add(time.Hour),
				Join: "tokenops team join " + base() + " tot_inv_x"})
		case "GET /api/v1/members":
			_ = json.NewEncoder(w).Encode([]teamwire.Member{
				{ID: "m1", Name: "Ada", Role: teamwire.RoleOwner, Teams: []string{"core"}, IsYou: true},
				{ID: "m2", Name: "Lars", Role: teamwire.RoleMember, Teams: []string{"core"}, Email: "lars@acme.example"},
			})
		case "DELETE /api/v1/members/m2":
			st.removed = "m2"
			_, _ = w.Write([]byte(`{"removed":true}`))
		case "PUT /api/v1/members/m2/role":
			var req teamwire.RoleRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			st.role = [2]string{"m2", req.Role}
			_, _ = w.Write([]byte(`{}`))
		case "GET /api/v1/grants":
			now := time.Now()
			_ = json.NewEncoder(w).Encode([]teamwire.Grant{
				{ID: "g1", Grantee: "Lars", Scope: "team core", Reason: "1:1s", GrantedBy: "Ada", GrantedAt: now},
				{ID: "g0", Grantee: "Old", Scope: "everyone", Reason: "gone", GrantedBy: "Ada", GrantedAt: now, RevokedAt: &now},
			})
		case "POST /api/v1/grants":
			_ = json.NewDecoder(r.Body).Decode(&st.grant)
			_ = json.NewEncoder(w).Encode(teamwire.Created{ID: "g2"})
		case "DELETE /api/v1/grants/g1":
			_, _ = w.Write([]byte(`{}`))
		case "GET /api/v1/billing":
			end := time.Now().Add(3 * 24 * time.Hour)
			_ = json.NewEncoder(w).Encode(teamwire.Billing{Plan: teamwire.Plan{Status: teamwire.PlanTrialing, TrialEndsAt: &end},
				Seats: 2, ManageURL: base() + "/billing"})
		case "POST /api/v1/billing/checkout":
			_ = json.NewEncoder(w).Encode(teamwire.BillingLink{URL: base() + "/billing?checkout=1"})
		case "POST /api/v1/billing/portal":
			_ = json.NewEncoder(w).Encode(teamwire.BillingLink{URL: "https://evil.example/portal"})
		case "GET /api/v1/audit":
			_ = json.NewEncoder(w).Encode([]teamwire.AuditEntry{{At: time.Now(), Actor: "Ada", Action: "team.create", Subject: "core"}})
		case "DELETE /api/v1/tokens/self":
			st.revoked = true
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected %s", route)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func newSelfServeStub(t *testing.T) (*httptest.Server, *selfServeStub, *[]string) {
	t.Helper()
	st := &selfServeStub{approve: 3}
	var srv *httptest.Server
	srv = httptest.NewServer(st.handler(t, func() string { return srv.URL }))
	t.Cleanup(srv.Close)
	var opened []string
	prevOpen, prevSleep := openBrowser, deviceLinkSleep
	openBrowser = func(u string) error { opened = append(opened, u); return nil }
	deviceLinkSleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { openBrowser, deviceLinkSleep = prevOpen, prevSleep })
	t.Setenv(adminTokenEnv, "")
	return srv, st, &opened
}

func TestTeamCreateLinksInBrowserAndAdministers(t *testing.T) {
	srv, st, opened := newSelfServeStub(t)
	dir := t.TempDir()
	state := filepath.Join(dir, "team.json")
	t.Setenv("TOKENOPS_TEAM_STATE", state)
	t.Setenv("TOKENOPS_STORAGE_PATH", filepath.Join(t.TempDir(), "events.db"))

	if _, err := runTeam(t, "create"); err == nil {
		t.Error("create without --server succeeded")
	}
	out, err := runTeam(t, "create", "--server", srv.URL, "--name", "Ada", "--device", "laptop")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	for _, want := range []string{"Your code: BCDF-GHJK", "joined Acme, team core", "administer it as owner",
		"Free trial until", "first week of team totals appears on", "team admin invite"} {
		if !strings.Contains(out, want) {
			t.Errorf("create output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, stubDevToken) || strings.Contains(out, stubAdminToken) || strings.Contains(out, stubDeviceCode) {
		t.Error("create printed a secret")
	}
	if !st.linkReq.Enroll || !st.linkReq.Admin || !st.linkReq.Create || st.linkReq.DeviceName != "laptop" {
		t.Errorf("link request %+v", st.linkReq)
	}
	if len(*opened) != 1 || *opened != nil && (*opened)[0] != srv.URL+"/link" || strings.Contains((*opened)[0], "BCDF") {
		t.Errorf("opened %v: want the link page without the code", *opened)
	}
	if st.polls != 3 {
		t.Errorf("polled %d times, want until approval on the 3rd", st.polls)
	}
	for _, p := range []string{state, teamclient.AdminStatePath(state)} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", p, info, err)
		}
	}
	if _, err := runTeam(t, "create", "--server", srv.URL); err == nil {
		t.Error("created while joined")
	}

	steps := []struct {
		args []string
		want string
	}{
		{[]string{"admin", "teams"}, "core"},
		{[]string{"admin", "create-team", "platform"}, "Created team platform"},
		{[]string{"admin", "invite", "--team", "core", "--role", "lead"}, "tokenops team join " + srv.URL + " tot_inv_x"},
		{[]string{"admin", "members"}, "Ada (you)"},
		{[]string{"admin", "role", "lars", "lead"}, "Lars is now lead"},
		{[]string{"admin", "grants"}, "1:1s"},
		{[]string{"admin", "grant", "Lars", "--team", "core", "--reason", "coaching"}, "grant g2"},
		{[]string{"admin", "revoke", "g1"}, "Revoked"},
		{[]string{"admin", "billing"}, "Seats: 2"},
		{[]string{"admin", "audit"}, "team.create"},
		{[]string{"admin", "remove", "lars@acme.example", "--yes"}, "Removed Lars"},
	}
	for _, s := range steps {
		out, err := runTeam(t, s.args...)
		if err != nil || !strings.Contains(out, s.want) {
			t.Errorf("%v: %v\n%s", s.args, err, out)
		}
	}
	if st.invite.Role != "lead" || st.invite.Team != "core" {
		t.Errorf("invite %+v", st.invite)
	}
	if st.role != [2]string{"m2", "lead"} || st.removed != "m2" || st.grant.GranteeID != "m2" || st.grant.Reason != "coaching" {
		t.Errorf("role %v removed %q grant %+v", st.role, st.removed, st.grant)
	}
	if out, _ := runTeam(t, "admin", "grants"); strings.Contains(out, "gone") {
		t.Error("revoked grant listed without --all")
	}

	// Guard rails.
	for _, args := range [][]string{
		{"admin", "remove", "Lars"},                      // needs --yes
		{"admin", "role", "Lars", "superuser"},           // not a role
		{"admin", "grant", "Lars", "--reason", "x"},      // no scope
		{"admin", "grant", "Lars", "--everyone"},         // no reason
		{"admin", "billing", "--portal", "--no-browser"}, // another site
		{"admin", "members", "--server", "https://other.example"},
	} {
		if out, err := runTeam(t, args...); err == nil {
			t.Errorf("%v succeeded:\n%s", args, out)
		}
	}
	*opened = nil
	out, err = runTeam(t, "admin", "billing", "--checkout")
	if err != nil || len(*opened) != 1 || !strings.HasPrefix((*opened)[0], srv.URL+"/billing") {
		t.Errorf("checkout: %v %v\n%s", err, *opened, out)
	}

	out, err = runTeam(t, "admin", "logout")
	if err != nil || !st.revoked || !strings.Contains(out, "revoked") {
		t.Errorf("logout: %v\n%s", err, out)
	}
	if _, err := os.Stat(teamclient.AdminStatePath(state)); !os.IsNotExist(err) {
		t.Error("admin credential kept after logout")
	}
	if _, err := runTeam(t, "admin", "teams"); err == nil {
		t.Error("admin command worked after logout")
	}
}

func TestTeamAdminTokenFromEnvironment(t *testing.T) {
	srv, _, _ := newSelfServeStub(t)
	t.Setenv("TOKENOPS_TEAM_STATE", filepath.Join(t.TempDir(), "team.json"))
	t.Setenv("TOKENOPS_STORAGE_PATH", filepath.Join(t.TempDir(), "events.db"))
	t.Setenv(adminTokenEnv, stubAdminToken)
	if _, err := runTeam(t, "admin", "teams"); err == nil {
		t.Error("token without a server accepted")
	}
	out, err := runTeam(t, "admin", "teams", "--server", srv.URL)
	if err != nil || !strings.Contains(out, "core") {
		t.Errorf("teams: %v\n%s", err, out)
	}
}

func TestTeamAdminLoginNeedsAdministrator(t *testing.T) {
	srv, st, _ := newSelfServeStub(t)
	state := filepath.Join(t.TempDir(), "team.json")
	t.Setenv("TOKENOPS_TEAM_STATE", state)
	t.Setenv("TOKENOPS_STORAGE_PATH", filepath.Join(t.TempDir(), "events.db"))
	out, err := runTeam(t, "admin", "login", "--server", srv.URL, "--no-browser")
	if err != nil || !strings.Contains(out, "Signed in to Acme as owner") {
		t.Fatalf("login: %v\n%s", err, out)
	}
	if st.linkReq.Enroll || !st.linkReq.Admin {
		t.Errorf("login asked for %+v", st.linkReq)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Error("admin login enrolled the machine")
	}
}

func TestTeamSyncExplainsPausedUploads(t *testing.T) {
	srv, st, _ := newSelfServeStub(t)
	st.paused = true
	state := filepath.Join(t.TempDir(), "team.json")
	t.Setenv("TOKENOPS_TEAM_STATE", state)
	t.Setenv("TOKENOPS_STORAGE_PATH", filepath.Join(t.TempDir(), "events.db"))
	if err := teamclient.Save(state, teamclient.State{URL: srv.URL, OrgName: "Acme", TeamName: "core", DeviceToken: stubDevToken}); err != nil {
		t.Fatal(err)
	}
	out, err := runTeam(t, "sync")
	if err == nil || !strings.Contains(err.Error(), "paused") || !strings.Contains(err.Error(), "team admin billing --checkout") {
		t.Fatalf("sync while paused: %v\n%s", err, out)
	}
	saved, _ := teamclient.Load(state)
	if !strings.HasPrefix(saved.LastResult, "paused") {
		t.Errorf("last result %q", saved.LastResult)
	}
	out, err = runTeam(t, "status")
	if err != nil || !strings.Contains(out, "Last upload") || !strings.Contains(out, "paused") {
		t.Errorf("status: %v\n%s", err, out)
	}
}

func TestBillingLinkHosts(t *testing.T) {
	base := "https://team.example.eu"
	for raw, ok := range map[string]bool{
		"https://team.example.eu/billing":                  true,
		"https://customer-portal.paddle.com/cpl_1":         true,
		"https://sandbox-customer-portal.paddle.com/cpl_1": true,
		"http://customer-portal.paddle.com/cpl_1":          false,
		"https://paddle.com.evil.example/":                 false,
		"https://evil.example/":                            false,
		"https://team.example.eu.evil.example/":            false,
		"javascript:alert(1)":                              false,
		"https://user@customer-portal.paddle.com/":         false,
	} {
		_, err := billingLink(base, raw)
		if (err == nil) != ok {
			t.Errorf("billingLink(%q) err=%v, want ok=%v", raw, err, ok)
		}
	}
}
