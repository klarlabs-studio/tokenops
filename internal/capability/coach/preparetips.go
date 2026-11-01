package coach

import (
	"fmt"
	"strings"
	"time"

	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

// PrepareTipInput is what tokenops_prepare_work knows about the work
// about to start, reduced to what the coach's tips need. It carries no
// instruction text.
type PrepareTipInput struct {
	// Recommendation is the routing advice's verdict ("stay" or
	// "switch"), Model the model it names and CurrentModel the one the
	// work would otherwise run on; Reason is the advice's reason.
	Recommendation string
	Model          string
	CurrentModel   string
	Reason         string
	// Share is the window-share suggestion: its action (proceed,
	// lighter_tier, defer, none), reason, and the window it measured.
	ShareAction string
	ShareReason string
	ShareWindow string
	// Windows are the plan windows the vendor reported.
	Windows []PrepareWindow
}

// PrepareWindow is one plan window as the tips read it.
type PrepareWindow struct {
	// Name names the window with its plan, as a tip says it:
	// "Claude 5h window".
	Name     string
	UsedPct  float64
	ResetsIn string
	// RunsOutIn is when the window runs out at the pace so far, zero
	// when it lasts to its reset or the pace is unknown.
	RunsOutIn time.Duration
}

// WindowLowPct is how full a plan window must be for prepare_work to tip
// that it is nearly used.
const WindowLowPct = 90

// PrepareWorkTips are the coach's tips for work about to start, each
// naming one object: the window the task may not fit, a window nearly
// used or running out before it resets, or the cheaper model the work
// fits. They are candidates; TipGate decides which are given.
func PrepareWorkTips(in PrepareTipInput) []DecisionTip {
	var out []DecisionTip
	named := map[string]bool{}
	if (in.ShareAction == "lighter_tier" || in.ShareAction == "defer") && in.ShareWindow != "" {
		named[in.ShareWindow] = true
		out = append(out, DecisionTip{
			Kind: TipWindowFit, Point: PointPrepareWork, Object: in.ShareWindow,
			Message: fmt.Sprintf("tokenops: this task may not fit the %s: %s.", in.ShareWindow, strings.TrimSuffix(in.ShareReason, ".")),
		})
	}
	for _, w := range in.Windows {
		if w.Name == "" || named[w.Name] {
			continue
		}
		msg := ""
		switch {
		case w.UsedPct >= WindowLowPct:
			msg = fmt.Sprintf("tokenops: the %s is %.0f%% used", w.Name, w.UsedPct)
		case w.RunsOutIn > 0:
			msg = fmt.Sprintf("tokenops: the %s runs out in %s at the pace so far", w.Name, shortDuration(w.RunsOutIn))
		default:
			continue
		}
		if w.ResetsIn != "" {
			msg += " (resets in " + w.ResetsIn + ")"
		}
		named[w.Name] = true
		out = append(out, DecisionTip{
			Kind: TipWindowLow, Point: PointPrepareWork, Object: w.Name,
			Message: msg + ". Keep this task to a small slice, or run it on a lighter model.",
		})
	}
	if in.Recommendation == "switch" && in.Model != "" && in.Model != in.CurrentModel {
		msg := fmt.Sprintf("tokenops: run this task on %s rather than %s", in.Model, in.CurrentModel)
		if r := strings.TrimSpace(in.Reason); r != "" {
			msg += ": " + strings.TrimSuffix(r, ".")
		}
		out = append(out, DecisionTip{Kind: TipModelFit, Point: PointPrepareWork, Object: in.Model, Message: msg + "."})
	}
	return out
}

func shortDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if h := int(d.Hours()); h > 0 {
		return fmt.Sprintf("%dh%02dm", h, int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// SettleWorkflowTips settles the model tip prepare_work gave for a
// workflow from the models its measured steps ran on: mostly on the model
// the tip named is following it; mostly elsewhere is ignoring it, with
// the model it ran on instead. With no measured steps nothing is settled.
func SettleWorkflowTips(l Ledger, now time.Time, workflowID string, models map[string]int) {
	if l == nil || workflowID == "" || len(models) == 0 {
		return
	}
	entries, err := l.Load()
	if err != nil {
		return
	}
	total, top, topN := 0, "", 0
	for m, n := range models {
		total += n
		if n > topN || (n == topN && m < top) {
			top, topN = m, n
		}
	}
	for _, r := range ft.Fold(entries, now) {
		o := r.Offer
		// Only a tip still open: one settled, or past its window, is not
		// settled again.
		if o.Ref != workflowID || o.Point != PointPrepareWork || o.Kind != TipModelFit || r.Outcome != ft.OutcomeOpen {
			continue
		}
		on := 0
		for m, n := range models {
			if sameModel(m, o.Object) {
				on += n
			}
		}
		if 2*on > total {
			RecordAgentOutcome(l, now, o.ID, o.Kind, true, "", fmt.Sprintf("%d of %d steps ran on %s", on, total, o.Object))
			continue
		}
		RecordAgentOutcome(l, now, o.ID, o.Kind, false, "ran on "+top, fmt.Sprintf("%d of %d steps ran on %s", on, total, o.Object))
	}
}

// sameModel compares a measured model with the one a tip named, allowing
// for a dated suffix on either ("claude-haiku-4-5-20251001").
func sameModel(measured, named string) bool {
	m, n := strings.ToLower(measured), strings.ToLower(named)
	return m == n || strings.HasPrefix(m, n+"-") || strings.HasPrefix(n, m+"-")
}
