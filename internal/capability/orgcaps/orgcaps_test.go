package orgcaps

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	anthropicapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/anthropic"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

type fakeReader struct {
	limits  []anthropicapi.SpendLimit
	pending []anthropicapi.IncreaseRequest
	err     error
}

func (f fakeReader) SpendLimits(context.Context) ([]anthropicapi.SpendLimit, error) {
	return f.limits, f.err
}
func (f fakeReader) PendingIncreaseRequests(context.Context) ([]anthropicapi.IncreaseRequest, error) {
	return f.pending, nil
}

func cents(v float64) *float64 { return &v }

func TestReadMatchesMembersAndOrdersByUrgency(t *testing.T) {
	asked := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	r := fakeReader{
		limits: []anthropicapi.SpendLimit{
			{UserID: "u1", Name: "Ann B.", Email: "ANN@example.com", Period: "monthly", Currency: "USD",
				Limit: cents(50000), Spent: 47500, Source: "seat_tier", SourceID: "enterprise_standard"},
			{UserID: "u2", Name: "Ben", Email: "ben@example.com", Period: "monthly", Currency: "USD",
				Limit: cents(20000), Spent: 2000, Source: "user"},
			{UserID: "u3", Name: "Cy", Email: "cy@contractor.example", Period: "monthly", Currency: "USD", Source: "organization"},
			{UserID: "u4", Email: "dee@example.com", Period: "monthly", Currency: "USD", Limit: cents(0), Source: "rbac_group", SourceID: "rbac_group_7"},
		},
		pending: []anthropicapi.IncreaseRequest{{ID: "slir_1", UserID: "u2", Period: "monthly", CreatedAt: asked}},
	}
	members := []teamwire.Member{
		{ID: "m1", Name: "Ann", Email: "ann@example.com"},
		{ID: "m2", Name: "Ben", Email: "ben@example.com"},
		{ID: "m4", Name: "Dee", Email: "dee@example.com"},
		{ID: "m5", Name: "Eve", Email: "eve@example.com"},
	}
	rep, err := Read(context.Background(), r, members)
	if err != nil {
		t.Fatal(err)
	}
	order := make([]string, 0, len(rep.Rows))
	for _, row := range rep.Rows {
		order = append(order, row.Member)
	}
	// Ben's request waits, then the fullest cap, then the rest by name.
	if got, want := strings.Join(order, ","), "Ben,Ann,Cy,Dee"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	ben, ann, cy, dee := rep.Rows[0], rep.Rows[1], rep.Rows[2], rep.Rows[3]
	if !ben.Pending || !ben.PendingSince.Equal(asked) || ben.Source != "set for them" || *ben.Limit != 200 {
		t.Errorf("ben = %+v", ben)
	}
	if ann.MemberID != "m1" || *ann.UsedPct != 95 || !ann.Near() || ann.Spent != 475 || ann.Source != "seat tier enterprise_standard" {
		t.Errorf("ann = %+v", ann)
	}
	if cy.MemberID != "" || cy.Limit != nil || cy.UsedPct != nil {
		t.Errorf("cy = %+v", cy)
	}
	if *dee.Limit != 0 || dee.UsedPct != nil || dee.Source != "group rbac_group_7" {
		t.Errorf("dee = %+v", dee)
	}
	if rep.NotInTeam != 1 || rep.Pending != 1 || strings.Join(rep.NoSeat, ",") != "Eve" {
		t.Errorf("report = not in team %d, pending %d, no seat %v", rep.NotInTeam, rep.Pending, rep.NoSeat)
	}
}

func TestReadNamesTheMissingScope(t *testing.T) {
	_, err := Read(context.Background(), fakeReader{err: anthropicapi.ErrSpendLimitsScope}, nil)
	if !errors.Is(err, anthropicapi.ErrSpendLimitsScope) || !strings.Contains(err.Error(), "Organization settings > API") {
		t.Fatalf("err = %v", err)
	}
}

func TestZeroDecimalCurrency(t *testing.T) {
	rep := assemble([]anthropicapi.SpendLimit{{UserID: "u", Name: "Kenji", Currency: "JPY", Limit: cents(30000), Spent: 15000}}, nil, nil)
	if r := rep.Rows[0]; *r.Limit != 30000 || r.Spent != 15000 || *r.UsedPct != 50 {
		t.Fatalf("row = %+v", r)
	}
}
