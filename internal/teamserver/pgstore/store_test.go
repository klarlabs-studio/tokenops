package pgstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/internal/contexts/team"
	"go.klarlabs.de/tokenops/internal/teamserver/pgstore"
	"go.klarlabs.de/tokenops/internal/teamserver/pgstore/pgtest"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

var monday = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

type fixture struct {
	s     *pgstore.Store
	org   pgstore.Org
	owner pgstore.Principal
}

func setup(t *testing.T) fixture {
	t.Helper()
	s := pgtest.New(t)
	s.SetClock(func() time.Time { return monday })
	org, owner, admin, login, err := s.CreateOrg(context.Background(), "Acme", "Olivia Owner")
	if err != nil {
		t.Fatal(err)
	}
	if admin == "" || login == "" {
		t.Fatal("no credentials minted")
	}
	return fixture{s: s, org: org, owner: owner}
}

func (f fixture) join(t *testing.T, teamName, name string, role team.Role) teamwire.EnrollResponse {
	t.Helper()
	ctx := context.Background()
	if _, err := f.s.TeamByRef(ctx, f.org.ID, teamName); errors.Is(err, pgstore.ErrNotFound) {
		if _, err := f.s.CreateTeam(ctx, f.owner, teamName); err != nil {
			t.Fatal(err)
		}
	}
	inv, _, err := f.s.CreateInvite(ctx, f.owner, teamName, role, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.s.Enroll(ctx, inv, name, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func upload(computed time.Time, days []string, buckets ...teamwire.Bucket) teamwire.Upload {
	return teamwire.Upload{Schema: 1, BatchID: uuid.NewString(), ComputedAt: computed, Days: days, Buckets: buckets}
}

func dev(r teamwire.EnrollResponse) pgstore.DeviceAuth {
	return pgstore.DeviceAuth{DeviceID: r.DeviceID, MemberID: r.MemberID, OrgID: r.OrgID}
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := pgtest.New(t)
	again, err := s.Migrate(context.Background())
	if err != nil || len(again) != 0 {
		t.Fatalf("second migrate applied %v, %v", again, err)
	}
}

func TestInviteIsSingleUseAndExpires(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.s.CreateTeam(ctx, f.owner, "platform"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CreateTeam(ctx, f.owner, "platform"); !errors.Is(err, pgstore.ErrConflict) {
		t.Errorf("duplicate team: %v", err)
	}
	inv, _, err := f.s.CreateInvite(ctx, f.owner, "platform", team.RoleMember, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.s.Enroll(ctx, inv, "Mia", "laptop")
	if err != nil || r.TeamName != "platform" || r.OrgName != "Acme" || r.DeviceToken == "" {
		t.Fatalf("enroll = %+v, %v", r, err)
	}
	if _, err := f.s.Enroll(ctx, inv, "Eve", "laptop"); !errors.Is(err, pgstore.ErrInviteInvalid) {
		t.Errorf("second use: %v", err)
	}
	old, _, _ := f.s.CreateInvite(ctx, f.owner, "platform", team.RoleMember, time.Hour)
	f.s.SetClock(func() time.Time { return monday.Add(2 * time.Hour) })
	if _, err := f.s.Enroll(ctx, old, "Late", "laptop"); !errors.Is(err, pgstore.ErrInviteInvalid) {
		t.Errorf("expired invite: %v", err)
	}
	got, err := f.s.AuthDevice(ctx, r.DeviceToken)
	if err != nil || got.MemberID != r.MemberID {
		t.Errorf("auth device = %+v, %v", got, err)
	}
	if _, err := f.s.AuthDevice(ctx, r.DeviceToken+"x"); !errors.Is(err, pgstore.ErrUnauthorized) {
		t.Errorf("wrong token: %v", err)
	}
}

func TestIngestIsIdempotentAndReplacesDays(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	m := f.join(t, "platform", "Mia", team.RoleMember)
	u := upload(monday, []string{"2026-10-05"},
		teamwire.Bucket{Day: "2026-10-05", Repo: "acme/api", Kind: "edit", Instructions: 4, Tokens: 100},
		teamwire.Bucket{Day: "2026-10-05", Repo: "acme/web", Kind: "deep", Instructions: 1, Tokens: 50})
	r, err := f.s.Ingest(ctx, dev(m), u)
	if err != nil || r.Accepted != 2 {
		t.Fatalf("ingest = %+v, %v", r, err)
	}
	r, err = f.s.Ingest(ctx, dev(m), u)
	if err != nil || !r.Duplicate || r.Accepted != 0 {
		t.Fatalf("replay = %+v, %v", r, err)
	}
	// A recomputation replaces the day whole: acme/web is gone.
	newer := upload(monday.Add(time.Hour), []string{"2026-10-05"},
		teamwire.Bucket{Day: "2026-10-05", Repo: "acme/api", Kind: "edit", Instructions: 5, Tokens: 120})
	if _, err := f.s.Ingest(ctx, dev(m), newer); err != nil {
		t.Fatal(err)
	}
	// An older computation arriving late changes nothing.
	late := upload(monday.Add(-time.Hour), []string{"2026-10-05"},
		teamwire.Bucket{Day: "2026-10-05", Repo: "acme/api", Kind: "edit", Instructions: 99})
	if r, err := f.s.Ingest(ctx, dev(m), late); err != nil || !r.Stale || r.Accepted != 0 {
		t.Fatalf("late = %+v, %v", r, err)
	}
	rows, err := f.s.MemberSeries(ctx, m.MemberID, team.Day, monday.AddDate(0, 0, -1), monday.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Totals.Instructions != 5 || rows[0].Totals.Tokens != 120 {
		t.Errorf("series = %+v", rows)
	}
}

func TestAggregateCountsPeople(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for i, name := range []string{"Ann", "Ben", "Cem"} {
		m := f.join(t, "platform", name, team.RoleMember)
		u := upload(monday, []string{"2026-10-05", "2026-10-06"},
			teamwire.Bucket{Day: "2026-10-05", Repo: "acme/api", Kind: "edit", Instructions: 2, FirstTry: 1, Tokens: int64(100 * (i + 1))},
			teamwire.Bucket{Day: "2026-10-06", Repo: "acme/api", Kind: "lookup", Instructions: 1, Tokens: 10})
		if _, err := f.s.Ingest(ctx, dev(m), u); err != nil {
			t.Fatal(err)
		}
	}
	solo := f.join(t, "data", "Dee", team.RoleMember)
	if _, err := f.s.Ingest(ctx, dev(solo), upload(monday, []string{"2026-10-05"},
		teamwire.Bucket{Day: "2026-10-05", Repo: "acme/etl", Kind: "deep", Instructions: 7})); err != nil {
		t.Fatal(err)
	}
	q := pgstore.Query{OrgID: f.org.ID, By: team.ByTeam, Period: team.Week, Since: monday.AddDate(0, 0, -7), Until: monday.AddDate(0, 0, 7)}
	rows, err := f.s.Aggregate(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	data, platform := rows[0], rows[1]
	if platform.Group != "platform" || platform.People != 3 || platform.Totals.Tokens != 630 || platform.Totals.Instructions != 9 {
		t.Errorf("platform = %+v", platform)
	}
	if data.People != 1 || team.Suppress(rows, f.org.MinGroupSize) != 1 || !rows[0].Suppressed {
		t.Errorf("a one-person team was not suppressed: %+v", rows)
	}
	q.By, q.Period = team.ByKind, team.Day
	rows, err = f.s.Aggregate(ctx, q)
	if err != nil || len(rows) != 3 {
		t.Fatalf("by kind = %+v, %v", rows, err)
	}
	platformTeam, _ := f.s.TeamByRef(ctx, f.org.ID, "platform")
	q.By, q.TeamID = team.ByRepo, platformTeam.ID
	rows, err = f.s.Aggregate(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Group != "acme/api" {
			t.Errorf("team filter leaked %s", r.Group)
		}
	}
}

func TestGrantsAreVisibleToTheMember(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	m := f.join(t, "platform", "Mia", team.RoleMember)
	lead := f.join(t, "platform", "Lars", team.RoleLead)
	if _, err := f.s.CreateGrant(ctx, f.owner, m.MemberID, "platform", "x"); err == nil {
		t.Error("a plain member was granted individual views")
	}
	id, err := f.s.CreateGrant(ctx, f.owner, lead.MemberID, "platform", "1:1 coaching, agreed with the works council")
	if err != nil {
		t.Fatal(err)
	}
	leadP, _ := f.s.Member(ctx, lead.MemberID)
	if err := f.s.RecordView(ctx, leadP, m.MemberID, id); err != nil {
		t.Fatal(err)
	}
	me, err := f.s.Me(ctx, m.MemberID)
	if err != nil {
		t.Fatal(err)
	}
	if len(me.Viewers) != 1 || me.Viewers[0].Name != "Lars" || me.Viewers[0].Scope != "team platform" {
		t.Errorf("viewers = %+v", me.Viewers)
	}
	if len(me.Views) != 1 || me.Views[0].Viewer != "Lars" {
		t.Errorf("views = %+v", me.Views)
	}
	if err := f.s.RevokeGrant(ctx, f.owner, id); err != nil {
		t.Fatal(err)
	}
	if active, _ := f.s.ActiveGrants(ctx, f.org.ID); len(active) != 0 {
		t.Errorf("revoked grant still active: %+v", active)
	}
	entries, _ := f.s.Audit(ctx, f.org.ID, 0)
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Action] = true
	}
	for _, a := range []string{"org.created", "team.created", "invite.created", "device.enrolled", "grant.created", "member.viewed", "grant.revoked"} {
		if !seen[a] {
			t.Errorf("audit lacks %s", a)
		}
	}
}

func TestLeaveAndRemoveEraseFigures(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	m := f.join(t, "platform", "Mia", team.RoleMember)
	if _, err := f.s.Ingest(ctx, dev(m), upload(monday, []string{"2026-10-05"},
		teamwire.Bucket{Day: "2026-10-05", Repo: "acme/api", Kind: "edit", Instructions: 1})); err != nil {
		t.Fatal(err)
	}
	self, _ := f.s.Member(ctx, m.MemberID)
	if err := f.s.RevokeDevice(ctx, self, m.DeviceID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AuthDevice(ctx, m.DeviceToken); !errors.Is(err, pgstore.ErrUnauthorized) {
		t.Errorf("revoked device still authenticates: %v", err)
	}
	if me, _ := f.s.Me(ctx, m.MemberID); me.Buckets != 0 {
		t.Errorf("erased device left %d buckets", me.Buckets)
	}
	if err := f.s.RemoveMember(ctx, f.owner, m.MemberID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Member(ctx, m.MemberID); !errors.Is(err, pgstore.ErrNotFound) {
		t.Errorf("removed member still resolves: %v", err)
	}
	if err := f.s.RemoveMember(ctx, f.owner, f.owner.MemberID); err == nil {
		t.Error("removed the last owner")
	}
}

func TestLoginLinkIsSingleUse(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	link, _, err := f.s.MintLogin(ctx, f.owner.MemberID)
	if err != nil {
		t.Fatal(err)
	}
	session, p, err := f.s.RedeemLogin(ctx, link)
	if err != nil || p.MemberID != f.owner.MemberID {
		t.Fatalf("redeem = %+v, %v", p, err)
	}
	if _, _, err := f.s.RedeemLogin(ctx, link); !errors.Is(err, pgstore.ErrUnauthorized) {
		t.Errorf("second redeem: %v", err)
	}
	if got, err := f.s.AuthToken(ctx, session, team.TokenSession); err != nil || got.Role != team.RoleOwner {
		t.Errorf("session = %+v, %v", got, err)
	}
	if _, err := f.s.AuthToken(ctx, session, team.TokenAdmin); err == nil {
		t.Error("a session token authenticated as an admin token")
	}
	f.s.SetClock(func() time.Time { return monday.Add(team.SessionTTL + time.Minute) })
	if _, err := f.s.AuthToken(ctx, session, team.TokenSession); !errors.Is(err, pgstore.ErrUnauthorized) {
		t.Errorf("expired session: %v", err)
	}
	if err := f.s.RevokeToken(ctx, session); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeHonoursRetention(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	m := f.join(t, "platform", "Mia", team.RoleMember)
	if _, err := f.s.Ingest(ctx, dev(m), upload(monday, []string{"2025-01-01", "2026-10-05"},
		teamwire.Bucket{Day: "2025-01-01", Repo: "acme/api", Kind: "edit", Instructions: 1},
		teamwire.Bucket{Day: "2026-10-05", Repo: "acme/api", Kind: "edit", Instructions: 1})); err != nil {
		t.Fatal(err)
	}
	r, err := f.s.Purge(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Buckets != 1 {
		t.Errorf("purged %+v", r)
	}
}
