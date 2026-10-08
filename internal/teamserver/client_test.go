package teamserver_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/capability/teamshare"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/internal/teamserver"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

type oneRepo struct{}

func (oneRepo) Root(context.Context, string) string       { return "/w/api" }
func (oneRepo) OriginName(context.Context, string) string { return "acme/api" }

// The daemon's path end to end: enrol with the client, build an upload
// from a machine's transcripts and turns, send it, read it back as the
// member, then leave.
func TestClientRoundTrip(t *testing.T) {
	e := newEnv(t)
	code, body := e.do("POST", "/api/v1/teams", e.admin, map[string]string{"name": "platform"})
	if code != 201 {
		t.Fatalf("team: %d %s", code, body)
	}
	_, body = e.do("POST", "/api/v1/invites", e.admin, map[string]any{"team": "platform"})
	var inv teamserver.InviteAnswer
	_ = json.Unmarshal(body, &inv)

	ctx := context.Background()
	resp, err := teamclient.New(e.srv.URL, "").Enroll(ctx, teamwire.EnrollRequest{Invite: inv.Invite, DisplayName: "Ann", DeviceName: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "team.json")
	if err := teamclient.Save(state, teamclient.State{URL: e.srv.URL, DeviceToken: resp.DeviceToken, TeamName: resp.TeamName}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	deps := teamshare.Deps{
		Units: func(_, _ time.Time) ([]sessions.Unit, error) {
			return []sessions.Unit{{SessionID: "s", Prompt: "add a retry to the client", Start: now.Add(-time.Hour), End: now.Add(-time.Hour + time.Minute), Turns: 2}}, nil
		},
		Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) {
			return []analytics.SessionTurn{{SessionID: "s", At: now.Add(-time.Hour + 30*time.Second), Tokens: 900, APIEquivalentUSD: 0.2, Priced: true}}, nil
		},
		Dirs:  func(time.Time) map[string]sessiondirs.Dir { return map[string]sessiondirs.Dir{"s": {CWD: "/w/api"}} },
		Repos: oneRepo{},
	}
	res, err := teamshare.Sync(ctx, deps, teamshare.Options{Days: 3}, state, now)
	if err != nil || res.Response.Accepted != 1 {
		t.Fatalf("sync = %+v, %v", res.Response, err)
	}
	st, _ := teamclient.Load(state)
	if st.LastUpload == nil || st.LastResult == "" {
		t.Errorf("attempt not recorded: %+v", st)
	}
	c := teamclient.New(e.srv.URL, resp.DeviceToken)
	me, err := c.Me(ctx)
	if err != nil || me.Buckets != 1 || me.Teams[0] != "platform" {
		t.Fatalf("me = %+v, %v", me, err)
	}
	_, body = e.do("GET", "/api/v1/members/"+resp.MemberID+"/metrics?period=day", resp.DeviceToken, nil)
	var ma teamserver.MemberAnswer
	_ = json.Unmarshal(body, &ma)
	if len(ma.Rows) != 1 || ma.Rows[0].Group != teamwire.KindEdit || ma.Rows[0].Totals.Tokens != 900 || ma.Recorded {
		t.Errorf("own figures = %s", body)
	}
	if link, err := c.LoginLink(ctx); err != nil || link.URL == "" {
		t.Errorf("login link = %+v, %v", link, err)
	}
	if err := c.Leave(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Me(ctx); err == nil {
		t.Error("left device still reads")
	}
}
