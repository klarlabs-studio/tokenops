package findings

import (
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/anomalies"
	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/contexts/spend/anomaly"
)

// shownContributors bounds the ranked contributors a finding names.
const shownContributors = 4

// anomalyFindings words each unusual day as one finding: ambient, never
// an interruption, and answered like a tip.
func anomalyFindings(as []anomalies.Anomaly) []Finding {
	out := make([]Finding, 0, len(as))
	for _, a := range as {
		out = append(out, Finding{
			ID:       a.ID,
			Kind:     KindSpend,
			Level:    LevelNotice,
			Title:    anomalyTitle(a.Anomaly),
			Evidence: anomalyEvidence(a),
			Action:   anomalyAction(a),
		})
	}
	return out
}

func anomalyTitle(a anomaly.Anomaly) string {
	what, prices := "Spend", ""
	if a.Metric == anomaly.MetricUsage {
		what, prices = "Usage", " at API prices"
	}
	over := fmt.Sprintf("%s over the usual %s", money(a.ExcessUSD), money(a.ExpectedUSD))
	if a.Partial {
		return fmt.Sprintf("%s today is already %s%s, %s", what, money(a.ActualUSD), prices, over)
	}
	verb := "was"
	if a.Metric == anomaly.MetricUsage {
		verb = "came to"
	}
	return fmt.Sprintf("%s on %s %s %s%s, %s", what, a.Day.Format("Mon 2 Jan"), verb, money(a.ActualUSD), prices, over)
}

func anomalyEvidence(a anomalies.Anomaly) string {
	var parts []string
	if c := causeWords(a); c != "" {
		parts = append(parts, "Likely cause: "+c)
	}
	var ranked []string
	for _, c := range a.Contributors[:min(len(a.Contributors), shownContributors)] {
		ranked = append(ranked, fmt.Sprintf("%s %s +%s (%.0f%%)", c.Dimension, c.Value, money(c.DeltaUSD), 100*c.Share))
	}
	if len(ranked) > 0 {
		parts = append(parts, "ranked by what each added: "+strings.Join(ranked, ", "))
	}
	return strings.Join(parts, "; ")
}

func causeWords(a anomalies.Anomaly) string {
	c := a.Cause
	switch c.Kind {
	case anomaly.CauseMeteredOnPlan:
		s := fmt.Sprintf("%s usage was billed per token while its plan should cover it", vendorOf(c.Value))
		if c.Endpoint != "" {
			s += ", through " + c.Endpoint
		}
		if c.CoveragePct != nil && c.BaselineCoveragePct != nil {
			s += fmt.Sprintf("; plan coverage %.0f%% against %.0f%% before", *c.CoveragePct, *c.BaselineCoveragePct)
		}
		return s
	case anomaly.CausePricierModel:
		return fmt.Sprintf("%s took over at %s per million tokens against %s before", c.Value, money(c.USDPerMTok), money(c.BaselineUSDPerMTok))
	case anomaly.CauseRunawayLoop:
		where := ""
		if c.Repo != "" {
			where = " in " + c.Repo
		}
		s := fmt.Sprintf("one session%s carried %.0f%% of the rise, as a runaway loop does", where, 100*c.Share)
		if len(a.Waste) > 0 {
			s += "; the waste detector found: " + strings.Join(a.Waste, ", ")
		}
		return s
	case anomaly.CauseSpike:
		if c.Value != "" {
			return fmt.Sprintf("most of the rise came from %s %s (%.0f%%)", c.Dimension, c.Value, 100*c.Share)
		}
	}
	return ""
}

// vendorOf names a provider as its users do.
func vendorOf(provider string) string {
	if v, ok := vendorNames[provider]; ok {
		return v
	}
	return provider
}

func anomalyAction(a anomalies.Anomaly) string {
	var do string
	switch a.Cause.Kind {
	case anomaly.CauseMeteredOnPlan:
		do = fmt.Sprintf("Check the API key or base URL your client uses: %s's plan covers only its own endpoint.", vendorOf(a.Cause.Value))
	case anomaly.CausePricierModel:
		do = "Check which model your client starts sessions on."
	case anomaly.CauseRunawayLoop:
		if a.Workflow != "" {
			do = fmt.Sprintf("`tokenops replay --workflow %s` shows the session step by step, with the waste detector's findings.", a.Workflow)
		} else {
			do = fmt.Sprintf("`tokenops replay %s` shows the session step by step.", a.Cause.Value)
		}
	}
	answer := fmt.Sprintf("If this is normal for you, `tokenops coach tip expected --id %s` stops flagging it; `ack` or `snooze` answer it too.", a.ID)
	return strings.TrimSpace(do + " " + answer)
}

// openAnomalies is what the daemon's last anomaly run still holds open,
// answers applied; nil when it has not run.
func openAnomalies(cacheDir string, l coach.Ledger, now time.Time) []anomalies.Anomaly {
	s, err := anomalies.ReadSnapshot(cacheDir)
	if err != nil || s == nil {
		return nil
	}
	return anomalies.Open(*s, l, now)
}
