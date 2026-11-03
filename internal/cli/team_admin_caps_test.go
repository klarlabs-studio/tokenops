package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/orgcaps"
)

func TestWriteCaps(t *testing.T) {
	limit, used, zero := 500.0, 95.0, 0.0
	rep := orgcaps.Report{Vendor: "Claude", Pending: 1, NotInTeam: 1, NoSeat: []string{"Eve"}, Rows: []orgcaps.Row{
		{Member: "Ann", MemberID: "m1", Period: "monthly", Currency: "USD", Limit: &limit, Spent: 475, UsedPct: &used,
			Source: "seat tier enterprise_standard", Pending: true, PendingSince: time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)},
		{Member: "Cy", Period: "monthly", Currency: "EUR", Spent: 12.5, Source: "organization"},
		{Member: "Dee", MemberID: "m4", Period: "monthly", Currency: "USD", Limit: &zero, Source: "group rbac_group_7"},
	}}
	var out bytes.Buffer
	if err := writeCaps(&out, rep); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Ann", "$500", "$475", "95% !", "waiting since",
		"Cy (not in the team)", "none", "12.50 EUR",
		"included only",
		"1 request(s) for a higher limit wait for you", "1 at 90% of their cap or more (!)",
		"1 Claude member(s) are not in the team", "no Claude limit for Eve", "Claude Enterprise spend limits",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	out.Reset()
	if err := writeCaps(&out, orgcaps.Report{Vendor: "Claude"}); err != nil || !strings.Contains(out.String(), "reports no spend limits") {
		t.Fatalf("empty report: %q, %v", out.String(), err)
	}
}

func TestWriteCapsCopilot(t *testing.T) {
	budget, used := 200.0, 40.0
	rep := orgcaps.Report{Vendor: "Copilot", Rows: []orgcaps.Row{
		{Member: "acme", Shared: true, Period: "monthly", Currency: "USD", Limit: &budget, Spent: 80, UsedPct: &used,
			Source: "organization budget on premium_requests (alerts only)"},
	}}
	var out bytes.Buffer
	if err := writeCaps(&out, rep); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "Copilot budgets") || !strings.Contains(got, "(alerts only)") || strings.Contains(got, "not in the team") {
		t.Fatalf("output:\n%s", got)
	}
}
