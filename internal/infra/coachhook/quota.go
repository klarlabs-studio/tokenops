package coachhook

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// Quota is the live rate-limit reading for the plan the session runs on:
// the most constrained window the vendor reports. When it is present the
// coach speaks about it instead of API-equivalent dollars, because on a
// flat-rate plan the dollar figure is a counterfactual and the window is
// what stops work.
type Quota struct {
	// Provider is the plan's provider ("anthropic", "openai").
	Provider string
	Window   plans.QuotaWindow
	// All is every window the reading reported, for the verbose summary.
	All []plans.QuotaWindow
}

// Verbosity levels (ADR 0006); empty reads as normal.
const (
	verbosityQuiet   = "quiet"
	verbosityVerbose = "verbose"
)

// DefaultQuotaTiers are the shares of a window at which the coach speaks,
// each once per window.
func DefaultQuotaTiers() []float64 { return []float64{0.50, 0.75, 0.90, 1.00} }

// quotaResetGranularity absorbs the fraction-of-a-second drift between two
// meter polls of the same window's reset time.
const quotaResetGranularity = 10 * time.Minute

// quotaKey identifies one window instance: a new reset is a new window.
func quotaKey(q *Quota) string {
	reset := q.Window.ResetsAt.UTC().Round(quotaResetGranularity)
	return q.Provider + "|" + q.Window.Label + "|" + reset.Format(time.RFC3339)
}

func quotaLatchFile(dir string) string { return filepath.Join(dir, "quota.json") }

// loadQuotaLatch reads the highest tier already said per window. Windows
// span sessions, so this lives beside the session files, not in them.
func loadQuotaLatch(dir string) map[string]float64 {
	m := map[string]float64{}
	if b, err := os.ReadFile(quotaLatchFile(dir)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// saveQuotaLatch keeps only windows that have not reset yet.
func saveQuotaLatch(dir string, m map[string]float64, now time.Time) {
	for k := range m {
		if reset, ok := latchReset(k); ok && now.After(reset.Add(quotaResetGranularity)) {
			delete(m, k)
		}
	}
	if b, err := json.Marshal(m); err == nil {
		_ = os.WriteFile(quotaLatchFile(dir), b, 0o600)
	}
}

func latchReset(key string) (time.Time, bool) {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '|' {
			t, err := time.Parse(time.RFC3339, key[i+1:])
			return t, err == nil
		}
	}
	return time.Time{}, false
}

// highestQuotaTier is the highest tier the window's share has reached that
// has not been said yet, or 0.
func highestQuotaTier(usedPct, latched float64, tiers []float64) float64 {
	frac := usedPct / 100
	fired := 0.0
	for _, t := range tiers {
		if frac >= t-fracEpsilon && t > latched+fracEpsilon && t > fired {
			fired = t
		}
	}
	return fired
}

// quotaMessage names the window, how much of it is gone, when it comes
// back, whether the current pace outruns it, and what stretches it.
func quotaMessage(q *Quota, now time.Time, verbosity string) string {
	w := q.Window
	name := fmt.Sprintf("%s %s limit", w.Label, providerName(q.Provider))
	resets := "resets in " + formatSpan(w.ResetsAt.Sub(now))
	if w.UsedPct >= 100 {
		return fmt.Sprintf("tokenops: your %s is used up (limit reached) — %s. "+
			"Work on this plan pauses until then; a smaller model or another plan keeps it moving.", name, resets)
	}
	msg := fmt.Sprintf("tokenops: %d%% of your %s used, %s.", int(math.Round(w.UsedPct)), name, resets)
	if at, runsOut := w.ProjectedExhaustion(now); runsOut {
		msg += fmt.Sprintf(" At this pace it runs out in ~%s, before the reset.", formatSpan(at.Sub(now)))
	} else if verbosity == verbosityVerbose {
		msg += " At this pace it lasts to the reset."
	}
	if verbosity == verbosityVerbose && len(q.All) > 1 {
		parts := make([]string, 0, len(q.All))
		for _, o := range q.All {
			parts = append(parts, fmt.Sprintf("%s %d%%", o.Label, int(math.Round(o.UsedPct))))
		}
		msg += " Windows: " + strings.Join(parts, ", ") + "."
	}
	return msg + " Route research and lookups to a smaller model and /compact long sessions to stretch it."
}

func providerName(p string) string {
	switch p {
	case "anthropic":
		return "Claude"
	case "openai":
		return "OpenAI"
	}
	return p
}

// formatSpan renders a duration the way a person reads a countdown.
func formatSpan(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d / (24 * time.Hour))
	hours := int(d%(24*time.Hour)) / int(time.Hour)
	mins := int(d%time.Hour) / int(time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// QuotaStatus is one line for `tokenops coach`: the live window the coach
// is judging, when it resets, and the share at which it will next speak.
// Reading the same latch the hook writes keeps the promise honest: a tier
// already said for this window is not offered again.
func QuotaStatus(dir string, q *Quota, verbosity string, now time.Time) string {
	if q == nil {
		return ""
	}
	w := q.Window
	line := fmt.Sprintf("%s %s %.0f%% · resets in %s", providerName(q.Provider), w.Label, w.UsedPct, formatSpan(w.ResetsAt.Sub(now)))
	next, ok := nextQuotaTier(loadQuotaLatch(resolveDir(dir))[quotaKey(q)], w.UsedPct/100, verbosity)
	switch {
	case ok:
		line += fmt.Sprintf(" · next tip at %.0f%%", next*100)
	case w.UsedPct < 100:
		line += " · every tip for this window already given"
	}
	return line
}

// nextQuotaTier is the lowest tier above both what was used and what was
// already said. Quiet only speaks from 90% (earlier only when the pace
// runs out before the reset, which a status line cannot promise).
func nextQuotaTier(latched, used float64, verbosity string) (float64, bool) {
	for _, t := range DefaultQuotaTiers() {
		if verbosity == verbosityQuiet && t < 0.90-fracEpsilon {
			continue
		}
		if t > latched+fracEpsilon && t > used+fracEpsilon {
			return t, true
		}
	}
	return 0, false
}
