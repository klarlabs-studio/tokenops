package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/providersetup"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
)

var (
	seventyLeft = regexp.MustCompile(`\b70(\.0)?% (left|of the \S+ left)`)
	thirtyUsed  = regexp.MustCompile(`\b30(\.0)?% (used|of the \S+ used)`)
)

// Plan windows read one way everywhere: the share left, counting down to
// "used up" like a fuel gauge ("Know what's left"). Each surface renders
// a window 30% used; it must say 70% left and never 30% used.
func TestPlanWindowsReadAsShareLeftOnEverySurface(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	thirty := 30.0
	meter := claudeusagemeter.UsageResponse{Windows: map[string]*claudeusagemeter.Window{"five_hour": {Utilization: &thirty}}}
	quota := &coachhook.Quota{Provider: "anthropic", Window: plans.QuotaWindow{Label: "weekly", UsedPct: 30, ResetsAt: now.Add(48 * time.Hour)}}

	surfaces := map[string]string{
		"tokenops plan (vendor windows)": strings.Join(planWindowLines(headroom.Report{Windows: []plans.VendorWindow{{Name: "week", UsedPct: 30, ResetsIn: "48h"}}}), "\n"),
		"tokenops plan (cap)":            strings.Join(planWindowLines(headroom.Report{WindowCap: 100, WindowConsumed: 30, WindowUnit: "messages", WindowPct: 30, WindowDuration: "5h", WindowResetsIn: "2h"}), "\n"),
		"tokenops plan (share only)":     strings.Join(planWindowLines(headroom.Report{WindowPct: 30, WindowDuration: "168h", WindowResetsIn: "48h"}), "\n"),
		"provider setup":                 strings.Join(providersetup.Summary(accounts.Reading{Windows: []accounts.Window{{Name: "weekly", UsedPct: 30}}}), "\n"),
		"usage meter":                    strings.Join(meter.Summary(), "\n"),
		"status line tip":                coachhook.Tip{Kind: "quota_30", Window: "Claude weekly"}.Short(now),
		"tokenops coach":                 coachhook.QuotaStatus(t.TempDir(), quota, now),
	}
	for name, got := range surfaces {
		if !seventyLeft.MatchString(got) {
			t.Errorf("%s does not say 70%% left: %q", name, got)
		}
		if thirtyUsed.MatchString(got) {
			t.Errorf("%s counts up as used: %q", name, got)
		}
	}
}

// A window with nothing left says so in words, not as 0% left.
func TestAPlanWindowWithNothingLeftIsUsedUp(t *testing.T) {
	got := strings.Join(planWindowLines(headroom.Report{Windows: []plans.VendorWindow{{Name: "week", UsedPct: 100}}}), "\n")
	if !strings.Contains(got, "the week used up") {
		t.Errorf("a full window = %q, want it used up", got)
	}
}

// The headroom pause is worded by what is left, as its rule is
// (plan_left_pct <= 10).
func TestHeadroomPauseIsWordedByWhatIsLeft(t *testing.T) {
	for _, args := range [][]string{{"boundaries", "headroom-pause", "--help"}, {"init", "--help"}} {
		root := NewRoot()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		text := strings.Join(strings.Fields(out.String()), " ")
		if strings.Contains(text, "90% used") || strings.Contains(text, "at 90% of") {
			t.Errorf("%v words the pause as used", args)
		}
		if !strings.Contains(text, "10% left") {
			t.Errorf("%v does not say 10%% left", args)
		}
	}
}
