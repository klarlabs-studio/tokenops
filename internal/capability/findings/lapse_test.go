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
