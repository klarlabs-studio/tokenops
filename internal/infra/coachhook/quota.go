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
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// Quota is the live rate-limit reading for the plan the session runs on.
// When it is present the coach speaks about it instead of API-equivalent
// dollars, because on a flat-rate plan the dollar figure is a
// counterfactual and the window is what stops work.
type Quota struct {
	// Provider is the plan's provider ("anthropic", "openai").
	Provider string
	// Window is the most constrained window: the one with the highest
	// share used.
	Window plans.QuotaWindow
	// All is every window the reading reported.
	All []plans.QuotaWindow
	// Burn is the burn-rate verdict on the windows (plans.AssessBurn):
	// the coach warns ahead of the limit only from it, never from a share
	// alone.
	Burn plans.BurnVerdict
}

// Verbosity levels (ADR 0006); empty reads as normal.
const (
	verbosityQuiet   = "quiet"
	verbosityVerbose = "verbose"
)

// burnKind names the run-out warning in the follow-through ledger.
const burnKind = "quota_burn"

// quotaResetGranularity absorbs the fraction-of-a-second drift between two
// meter polls of the same window's reset time.
const quotaResetGranularity = 10 * time.Minute

// windowKey identifies one window instance: a new reset is a new window.
func windowKey(provider string, w plans.QuotaWindow) string {
	reset := w.ResetsAt.UTC().Round(quotaResetGranularity)
	return provider + "|" + w.Label + "|" + reset.Format(time.RFC3339)
}

// windowWatch is what the coach knows about one window instance across
// sessions: what it said about it, and what became of it.
type windowWatch struct {
	Provider string    `json:"provider"`
	Label    string    `json:"label"`
	ResetsAt time.Time `json:"resets_at"`
	// PeakPct is the highest share the coach saw used.
	PeakPct float64 `json:"peak_pct"`
	// Forecast is set once the burn rule said it runs out before the
	// reset, whether or not the coach was allowed to say so.
	Forecast bool `json:"forecast,omitempty"`
	// Warned and UsedUpSaid record what was said (or dropped by the
	// quiet cap), so neither is said twice.
	Warned     bool `json:"warned,omitempty"`
	UsedUpSaid bool `json:"used_up_said,omitempty"`
}

func watchFile(dir string) string   { return filepath.Join(dir, "windows.json") }
func outcomeFile(dir string) string { return filepath.Join(dir, "run-outs.jsonl") }
func (w windowWatch) ranOut() bool  { return w.PeakPct >= 100 }
func (w windowWatch) settled(now time.Time) bool {
	return now.After(w.ResetsAt.Add(quotaResetGranularity))
}

// loadWatches reads the per-window record. Windows span sessions, so it
// lives beside the session files, not in them.
func loadWatches(dir string) map[string]*windowWatch {
	m := map[string]*windowWatch{}
	if b, err := os.ReadFile(watchFile(dir)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// saveWatches settles every window that has reset into the run-out record
// and keeps the rest.
func saveWatches(dir string, m map[string]*windowWatch, now time.Time) {
	for k, w := range m {
		if w.settled(now) {
			appendRunOut(dir, runOutRecord{
				Provider: w.Provider, Label: w.Label, ResetsAt: w.ResetsAt, PeakPct: w.PeakPct,
				Forecast: w.Forecast, Warned: w.Warned, RanOut: w.ranOut(),
			})
			delete(m, k)
		}
	}
	if b, err := json.Marshal(m); err == nil {
		_ = os.WriteFile(watchFile(dir), b, 0o600)
	}
}

// observeWindows updates each window's peak and whether the burn rule has
// forecast it to run out.
func observeWindows(m map[string]*windowWatch, q *Quota) {
	get := func(w plans.QuotaWindow) *windowWatch {
		k := windowKey(q.Provider, w)
		if m[k] == nil {
			m[k] = &windowWatch{Provider: q.Provider, Label: w.Label, ResetsAt: w.ResetsAt.UTC()}
		}
		return m[k]
	}
	for _, w := range append([]plans.QuotaWindow{q.Window}, q.All...) {
		if w.Label == "" || w.ResetsAt.IsZero() {
			continue
		}
		if e := get(w); w.UsedPct > e.PeakPct {
			e.PeakPct = w.UsedPct
		}
	}
	for _, b := range q.Burn.Warnings {
		get(b.Window).Forecast = true
	}
}

// evaluateQuota speaks at most one thing about the plan's windows: that the
// most constrained one is used up, or that both look-backs run a window out
// before its reset. Each once per window, across sessions. A share alone
// says nothing: 90% ten minutes before the reset lasts, 50% half an hour
// into a five-hour window does not.
func evaluateQuota(dir string, dec *Decision, st *sessionState, cfg Config, model string, contextTokens int64, now time.Time) {
	q := cfg.Quota
	watches := loadWatches(dir)
	observeWindows(watches, q)
	defer saveWatches(dir, watches, now)
	if !cfg.Enabled {
		return
	}
	kind, w, warning, entry := pickQuotaTip(q, watches)
	if kind == "" {
		return
	}
	reason, retry := cfg.hold(kind, st, now)
	switch {
	case reason == "":
		dec.Nudge = true
		dec.quotaWindow = w
		if kind == burnKind {
			dec.QuotaBurn = true
			dec.runsOutAt = warning.RunsOutAt
			dec.Message = burnMessage(q, warning, now, cfg.Verbosity)
		} else {
			dec.QuotaTier = 1.0
			dec.Message = usedUpMessage(q.Provider, w, now)
		}
		if note := contextNote(contextTokens, model); note != "" {
			dec.Message = note + " " + dec.Message
		}
		dec.ContextTokens = contextTokens
		if cw, ok := spend.ContextWindow(model); ok {
			dec.ContextWindow = cw
		}
		st.LastNudgeAt = now.UTC().Format(time.RFC3339Nano)
		st.Nudges++
	case retry:
		dec.Suppressed = reason
		return
	default:
		dec.Suppressed = reason
	}
	if kind == burnKind {
		entry.Warned = true
	} else {
		entry.UsedUpSaid = true
	}
}

// pickQuotaTip is what the coach would say now and the window it is about,
// or "" when there is nothing new to say.
func pickQuotaTip(q *Quota, watches map[string]*windowWatch) (string, plans.QuotaWindow, plans.BurnWarning, *windowWatch) {
	if q.Window.UsedPct >= 100 {
		if e := watches[windowKey(q.Provider, q.Window)]; e != nil && !e.UsedUpSaid {
			return quotaKind(1.0), q.Window, plans.BurnWarning{}, e
		}
		return "", plans.QuotaWindow{}, plans.BurnWarning{}, nil
	}
	if len(q.Burn.Warnings) == 0 {
		return "", plans.QuotaWindow{}, plans.BurnWarning{}, nil
	}
	b := q.Burn.Warnings[0]
	if e := watches[windowKey(q.Provider, b.Window)]; e != nil && !e.Warned {
		return burnKind, b.Window, b, e
	}
	return "", plans.QuotaWindow{}, plans.BurnWarning{}, nil
}

func windowName(provider string, w plans.QuotaWindow) string {
	return fmt.Sprintf("%s %s limit", w.Label, providerName(provider))
}

func usedUpMessage(provider string, w plans.QuotaWindow, now time.Time) string {
	return fmt.Sprintf("tokenops: your %s is used up (limit reached) — resets in %s. "+
		"Work on this plan pauses until then; a smaller model or another plan keeps it moving.",
		windowName(provider, w), formatSpan(w.ResetsAt.Sub(now)))
}

// burnMessage says when the window runs out at the pace of both look-backs,
// how long before its reset, and what stretches it.
func burnMessage(q *Quota, b plans.BurnWarning, now time.Time, verbosity string) string {
	w := b.Window
	msg := fmt.Sprintf("tokenops: at this pace your %s runs out in ~%s, %s before it resets (%d%% used).",
		windowName(q.Provider, w), formatSpan(b.RunsOutAt.Sub(now)), formatSpan(b.BeforeReset()), int(math.Round(w.UsedPct)))
	if verbosity == verbosityVerbose {
		msg += fmt.Sprintf(" That is %.1f× the even pace over the last hour and %.1f× over the last %d minutes.",
			b.LongBurn, b.ShortBurn, int(plans.BurnShortLookback/time.Minute))
		if len(q.All) > 1 {
			parts := make([]string, 0, len(q.All))
			for _, o := range q.All {
				parts = append(parts, fmt.Sprintf("%s %d%%", o.Label, int(math.Round(o.UsedPct))))
			}
			msg += " Windows: " + strings.Join(parts, ", ") + "."
		}
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

// QuotaStatus is one line for `tokenops coach`: the live window, when it
// resets, and what the run-out rule makes of it.
func QuotaStatus(dir string, q *Quota, now time.Time) string {
	if q == nil {
		return ""
	}
	w := q.Window
	line := fmt.Sprintf("%s %s %.0f%% · resets in %s", providerName(q.Provider), w.Label, w.UsedPct, formatSpan(w.ResetsAt.Sub(now)))
	switch {
	case w.UsedPct >= 100:
	case q.Burn.Stale:
		line += " · reading too old to forecast a run-out"
	case len(q.Burn.Warnings) > 0:
		b := q.Burn.Warnings[0]
		line += fmt.Sprintf(" · %s runs out in ~%s at this pace, %s before the reset",
			b.Window.Label, formatSpan(b.RunsOutAt.Sub(now)), formatSpan(b.BeforeReset()))
		if e := loadWatches(resolveDir(dir))[windowKey(q.Provider, b.Window)]; e != nil && e.Warned {
			line += " (warned)"
		}
	default:
		line += " · not on course to run out before the reset"
	}
	return line
}

func quotaLabel(q *Quota, spoken plans.QuotaWindow) string {
	if q == nil {
		return ""
	}
	if spoken.Label != "" {
		return q.Provider + " " + spoken.Label
	}
	return q.Provider + " " + q.Window.Label
}

func quotaUsed(q *Quota, spoken plans.QuotaWindow) float64 {
	if q == nil {
		return 0
	}
	if spoken.Label != "" {
		return spoken.UsedPct
	}
	return q.Window.UsedPct
}

// runOutRecord is one settled window: whether the burn rule forecast it to
// run out, whether the coach said so, and whether it did. Fired against
// ran out is the warning's precision.
type runOutRecord struct {
	Provider string    `json:"provider"`
	Label    string    `json:"label"`
	ResetsAt time.Time `json:"resets_at"`
	PeakPct  float64   `json:"peak_pct"`
	Forecast bool      `json:"forecast"`
	Warned   bool      `json:"warned"`
	RanOut   bool      `json:"ran_out"`
}

func appendRunOut(dir string, r runOutRecord) {
	f, err := os.OpenFile(outcomeFile(dir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // TokenOps' own state
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_ = json.NewEncoder(f).Encode(r)
}

// RunOutScore scores the run-out forecasts over the windows that have
// reset. A window counts as run out when the coach saw it at 100%.
type RunOutScore struct {
	Windows          int `json:"windows"`
	Forecast         int `json:"forecast"`
	ForecastRanOut   int `json:"forecast_ran_out"`
	RanOutUnforecast int `json:"ran_out_unforecast"`
}

// Precision is the share of forecasts that came true; ok is false before
// any forecast.
func (s RunOutScore) Precision() (float64, bool) {
	if s.Forecast == 0 {
		return 0, false
	}
	return float64(s.ForecastRanOut) / float64(s.Forecast), true
}

func readRunOuts(dir string) RunOutScore {
	var s RunOutScore
	f, err := os.Open(outcomeFile(dir)) //nolint:gosec // TokenOps' own state
	if err != nil {
		return s
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(f)
	for {
		var r runOutRecord
		if err := dec.Decode(&r); err != nil {
			break
		}
		s.Windows++
		switch {
		case r.Forecast && r.RanOut:
			s.Forecast++
			s.ForecastRanOut++
		case r.Forecast:
			s.Forecast++
		case r.RanOut:
			s.RanOutUnforecast++
		}
	}
	return s
}
