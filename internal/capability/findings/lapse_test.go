package findings

import (
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// A weekly window about to reset with much of it unused is said once a
// day ahead: unused headroom does not carry over. One nearly used, one
// with days to go, or a 5-hour window, is not.
func TestQuotaSaysWhenWeeklyHeadroomIsAboutToLapse(t *testing.T) {
	week := func(used float64, resetsIn time.Duration) plans.VendorWindow {
		return plans.VendorWindow{Name: "week", UsedPct: used, Duration: 7 * 24 * time.Hour, ResetsIn: resetsIn.String(),
			Pace: &plans.WindowPace{Status: plans.PaceBehind, LastsToReset: true}}
	}
	reports := []plans.HeadroomReport{
		{Provider: "anthropic", Windows: []plans.VendorWindow{week(38, 14*time.Hour)}},
		{Provider: "openai", Windows: []plans.VendorWindow{week(85, 10*time.Hour)}},
		{Provider: "cursor", Windows: []plans.VendorWindow{week(10, 3*24*time.Hour)}},
		{Provider: "opencode", Windows: []plans.VendorWindow{{Name: "5h", UsedPct: 5, Duration: 5 * time.Hour, ResetsIn: "1h0m0s",
			Pace: &plans.WindowPace{Status: plans.PaceBehind, LastsToReset: true}}}},
	}
	var lapses []Finding
	for _, f := range quota(reports) {
		if strings.HasSuffix(f.Key, "lapse") {
			lapses = append(lapses, f)
		}
	}
	if len(lapses) != 1 {
		t.Fatalf("lapse findings = %+v", lapses)
	}
	f := lapses[0]
	if f.Provider != "anthropic" || f.Level != LevelInfo || !strings.Contains(f.Title, "62% unused") || !strings.Contains(f.Title, "14h") {
		t.Errorf("finding = %+v", f)
	}
	if f.Action == "" {
		t.Error("no action")
	}
}

// A banked allowance about to expire unused is said in the lapse finding:
// folded into a lapsing week's, or under the week's key on its own, so
// there is never a second alert about the same lapse. One expiring after
// the horizon, or with no expiry, says nothing.
func TestQuotaFoldsExpiringAllowancesIntoTheLapseFinding(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	week := func(used float64, resetsIn time.Duration) plans.VendorWindow {
		return plans.VendorWindow{Name: "week", UsedPct: used, Duration: 7 * 24 * time.Hour, ResetsIn: resetsIn.String(),
			Pace: &plans.WindowPace{Status: plans.PaceBehind, LastsToReset: true}}
	}
	credit := func(expiresIn time.Duration) plans.Allowance {
		return plans.Allowance{Kind: plans.AllowanceResetCredit, ID: "rc", GrantedAt: now.Add(-48 * time.Hour),
			ExpiresAt: now.Add(expiresIn), Source: "codex-app-server", ObservedAt: now.Add(-10 * time.Minute)}
	}
	tests := []struct {
		name       string
		report     plans.HeadroomReport
		wantLapses int
		title      []string
		action     string
	}{
		{
			name: "folded into a lapsing week",
			report: plans.HeadroomReport{Provider: "openai", Windows: []plans.VendorWindow{week(38, 14*time.Hour)},
				Allowances: &plans.Allowances{Available: 1, Items: []plans.Allowance{credit(6 * time.Hour)}}},
			wantLapses: 1, title: []string{"62% unused", "1 reset credit expires in 6h 0m unused"},
		},
		{
			name: "on its own, under the week's key",
			report: plans.HeadroomReport{Provider: "openai", Windows: []plans.VendorWindow{week(85, 3*24*time.Hour)},
				Allowances: &plans.Allowances{Available: 2, Items: []plans.Allowance{credit(2 * time.Hour), credit(20 * time.Hour)}}},
			wantLapses: 1, title: []string{"Codex", "2 reset credits expire within 20h 0m unused", "first in 2h 0m"},
			action: "85%",
		},
		{
			name: "expiring after the horizon",
			report: plans.HeadroomReport{Provider: "openai", Windows: []plans.VendorWindow{week(85, 3*24*time.Hour)},
				Allowances: &plans.Allowances{Available: 1, Items: []plans.Allowance{credit(3 * 24 * time.Hour)}}},
		},
		{
			name: "no expiry",
			report: plans.HeadroomReport{Provider: "openai", Windows: []plans.VendorWindow{week(85, 3*24*time.Hour)},
				Allowances: &plans.Allowances{Available: 1, Items: []plans.Allowance{{Kind: plans.AllowanceResetCredit}}}},
		},
		{
			name: "no windows at all",
			report: plans.HeadroomReport{Provider: "openai",
				Allowances: &plans.Allowances{Available: 1, Items: []plans.Allowance{credit(time.Hour)}}},
			wantLapses: 1, title: []string{"1 reset credit expires in 1h 0m unused"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var lapses []Finding
			for _, f := range quotaAt([]plans.HeadroomReport{tc.report}, now) {
				if strings.HasSuffix(f.Key, "lapse") {
					lapses = append(lapses, f)
				}
			}
			if len(lapses) != tc.wantLapses {
				t.Fatalf("lapse findings = %+v", lapses)
			}
			if tc.wantLapses == 0 {
				return
			}
			f := lapses[0]
			for _, want := range tc.title {
				if !strings.Contains(f.Title, want) {
					t.Errorf("title %q lacks %q", f.Title, want)
				}
			}
			if len(tc.report.Windows) > 0 && f.Key != "quota:openai:week:lapse" {
				t.Errorf("key = %s, want the week's lapse key", f.Key)
			}
			if !strings.Contains(f.Evidence, "lost") || f.Level != LevelInfo || f.Action == "" {
				t.Errorf("finding = %+v", f)
			}
			if tc.action != "" && !strings.Contains(f.Action, tc.action) {
				t.Errorf("action %q lacks %q", f.Action, tc.action)
			}
		})
	}
}
