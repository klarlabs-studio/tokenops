package plans

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// QuotaWindow is one vendor-reported rate-limit window: how much of it is
// used and when it resets. On a flat-rate plan this, not a dollar figure,
// is the number that stops work.
type QuotaWindow struct {
	// Label names the window for a person: "5-hour", "weekly",
	// "weekly Fable" for a model-scoped weekly limit.
	Label    string
	UsedPct  float64
	ResetsAt time.Time
	Duration time.Duration
}

const week = 7 * 24 * time.Hour

// QuotaWindowsFromAttributes reads every window a vendor meter reading
// reports. Anthropic's claude.ai meter records `<name>_used_pct`,
// `<name>_kind`, and `<name>_reset_at` per window, including model-scoped
// weekly limits; Codex records a primary and secondary window with their
// length in minutes. A key without a reset time or a window length is not
// a window: its denominator is unknown, so no share of it can be claimed.
func QuotaWindowsFromAttributes(provider eventschema.Provider, attrs map[string]string) []QuotaWindow {
	var out []QuotaWindow
	switch provider {
	case eventschema.ProviderAnthropic:
		for key, raw := range attrs {
			name, ok := strings.CutSuffix(key, "_used_pct")
			if !ok {
				continue
			}
			used, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				continue
			}
			reset, err := time.Parse(time.RFC3339Nano, attrs[name+"_reset_at"])
			if err != nil {
				continue
			}
			label, dur, ok := claudeWindow(name, attrs[name+"_kind"], attrs[name+"_model_scope"])
			if !ok {
				continue
			}
			out = append(out, QuotaWindow{Label: label, UsedPct: used, ResetsAt: reset, Duration: dur})
		}
	case eventschema.ProviderOpenAI:
		for _, slot := range []string{"primary", "secondary"} {
			used, err := strconv.ParseFloat(attrs[slot+"_used_pct"], 64)
			if err != nil {
				continue
			}
			minutes, _ := strconv.ParseInt(attrs[slot+"_window_min"], 10, 64)
			resetUnix, _ := strconv.ParseInt(attrs[slot+"_resets_at"], 10, 64)
			if minutes <= 0 || resetUnix <= 0 {
				continue
			}
			dur := time.Duration(minutes) * time.Minute
			out = append(out, QuotaWindow{Label: windowLabel(dur), UsedPct: used, ResetsAt: time.Unix(resetUnix, 0).UTC(), Duration: dur})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// claudeWindow maps a claude.ai window to its label and length.
func claudeWindow(name, kind, scope string) (string, time.Duration, bool) {
	switch {
	case name == "five_hour" || kind == "session":
		return "5-hour", 5 * time.Hour, true
	case name == "seven_day" || kind == "weekly_all":
		return "weekly", week, true
	case strings.HasPrefix(kind, "weekly"):
		if scope != "" {
			return "weekly " + scope, week, true
		}
		return "weekly", week, true
	}
	return "", 0, false
}

func windowLabel(d time.Duration) string {
	switch {
	case d == week:
		return "weekly"
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%d-day", int(d/(24*time.Hour)))
	default:
		return fmt.Sprintf("%d-hour", int(d.Round(time.Hour)/time.Hour))
	}
}

// MostConstrained returns the window with the highest share used: the one
// that will stop work first if nothing changes.
func MostConstrained(ws []QuotaWindow) (QuotaWindow, bool) {
	if len(ws) == 0 {
		return QuotaWindow{}, false
	}
	best := ws[0]
	for _, w := range ws[1:] {
		if w.UsedPct > best.UsedPct {
			best = w
		}
	}
	return best, true
}

// ProjectedExhaustion extrapolates the window's use so far at a constant
// rate and reports when it would reach 100%, and whether that is before
// the window resets. Nothing used, or no elapsed time to take a rate from,
// never runs out.
func (w QuotaWindow) ProjectedExhaustion(now time.Time) (time.Time, bool) {
	elapsed := w.Duration - w.ResetsAt.Sub(now)
	if w.UsedPct <= 0 || elapsed <= 0 || w.Duration <= 0 {
		return time.Time{}, false
	}
	if w.UsedPct >= 100 {
		return now, true
	}
	perPct := float64(elapsed) / w.UsedPct
	at := now.Add(time.Duration(perPct * (100 - w.UsedPct)))
	return at, at.Before(w.ResetsAt)
}
