package team

import (
	"bytes"
	"testing"
	"time"
)

func TestCanViewMember(t *testing.T) {
	revoked := time.Now()
	grants := []Grant{
		{ID: "g1", GranteeID: "lead", TeamID: "t-a"},
		{ID: "g2", GranteeID: "old", TeamID: "", RevokedAt: &revoked},
		{ID: "g3", GranteeID: "demoted", TeamID: ""},
		{ID: "g4", GranteeID: "owner", TeamID: ""},
	}
	cases := []struct {
		name    string
		viewer  string
		role    Role
		subject string
		teams   []string
		want    Access
	}{
		{"self", "m1", RoleMember, "m1", nil, Access{Allowed: true, Self: true}},
		{"granted team", "lead", RoleLead, "m1", []string{"t-b", "t-a"}, Access{Allowed: true, GrantID: "g1"}},
		{"other team", "lead", RoleLead, "m1", []string{"t-b"}, Access{}},
		{"revoked", "old", RoleOwner, "m1", []string{"t-a"}, Access{}},
		{"role no longer holds grants", "demoted", RoleMember, "m1", []string{"t-a"}, Access{}},
		{"owner without grant", "boss", RoleOwner, "m1", []string{"t-a"}, Access{}},
		{"owner with org grant", "owner", RoleOwner, "m1", []string{"t-z"}, Access{Allowed: true, GrantID: "g4"}},
	}
	for _, c := range cases {
		if got := CanViewMember(c.viewer, c.role, c.subject, c.teams, grants); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestRoles(t *testing.T) {
	if _, err := ParseRole("root"); err == nil {
		t.Error("ParseRole accepted root")
	}
	if !CanInvite(RoleAdmin, RoleLead) || CanInvite(RoleAdmin, RoleOwner) || CanInvite(RoleLead, RoleMember) {
		t.Error("CanInvite")
	}
	if RoleAdmin.CanGrant() || !RoleOwner.CanGrant() || RoleMember.CanHoldGrant() || !RoleLead.CanHoldGrant() {
		t.Error("grant rules")
	}
}

func TestPeriodStart(t *testing.T) {
	thu := time.Date(2026, 10, 8, 15, 4, 0, 0, time.UTC)
	if got := Week.Start(thu); !got.Equal(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("week start = %v", got)
	}
	sun := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	if got := Week.Start(sun); !got.Equal(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("sunday's week start = %v", got)
	}
	if got := Day.Start(thu); !got.Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("day start = %v", got)
	}
}

func TestSuppress(t *testing.T) {
	rows := []Row{
		{Group: "a", People: 2, Totals: Totals{Tokens: 5}},
		{Group: "b", People: 3, Totals: Totals{Tokens: 7}},
	}
	if n := Suppress(rows, 3); n != 1 {
		t.Fatalf("suppressed %d", n)
	}
	if !rows[0].Suppressed || rows[0].Totals.Tokens != 0 || rows[1].Suppressed || rows[1].Totals.Tokens != 7 {
		t.Errorf("rows = %+v", rows)
	}
}

func TestRates(t *testing.T) {
	var tot Totals
	tot.Add(Totals{Instructions: 2, FirstTry: 1, Turns: 6, APIEquivalentUSD: 1, ActiveSeconds: 120})
	tot.Add(Totals{Instructions: 2, FirstTry: 2, Turns: 2, APIEquivalentUSD: 1, ActiveSeconds: 120})
	r := tot.Rates()
	if r.FirstTryPct != 75 || r.TurnsPerInstruction != 2 || r.CostPerInstruction != 0.5 || r.MinutesPerInstr != 1 {
		t.Errorf("rates = %+v", r)
	}
	if (Totals{}).Rates() != (Rates{}) {
		t.Error("empty totals have rates")
	}
}

func TestTokens(t *testing.T) {
	plain, hash, err := NewToken(TokenDevice)
	if err != nil {
		t.Fatal(err)
	}
	if k, err := KindOf(plain); err != nil || k != TokenDevice {
		t.Errorf("KindOf(%q) = %v, %v", plain, k, err)
	}
	if !bytes.Equal(hash, HashToken(plain)) || bytes.Contains(hash, []byte(plain)) {
		t.Error("hash")
	}
	other, _, _ := NewToken(TokenDevice)
	if other == plain {
		t.Error("two tokens are equal")
	}
	for _, bad := range []string{"", "tot_xyz_abc", "tot_dev_short", "bearer " + plain, plain + "x", "tot_dev_" + string(make([]byte, 52))} {
		if _, err := KindOf(bad); err == nil {
			t.Errorf("KindOf accepted %q", bad)
		}
	}
}

func TestRetentionCutoff(t *testing.T) {
	now := time.Date(2026, 10, 8, 23, 0, 0, 0, time.UTC)
	if got := RetentionCutoff(now, 1); !got.Equal(time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("clamped cutoff = %v", got)
	}
	if got := RetentionCutoff(now, 400); !got.Equal(now.Truncate(24*time.Hour).AddDate(0, 0, -400)) {
		t.Errorf("cutoff = %v", got)
	}
}
