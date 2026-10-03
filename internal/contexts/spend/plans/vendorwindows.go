package plans

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// VendorWindow is one usage window as the vendor reported it: a share
// used and when it resets. Vendors report percentages, not counts.
type VendorWindow struct {
	// Name is the window in words: "5h", "week", "week (Fable)".
	Name     string        `json:"name"`
	UsedPct  float64       `json:"used_pct"`
	Duration time.Duration `json:"duration_ns,omitempty"`
	ResetsAt time.Time     `json:"resets_at,omitempty"`
	ResetsIn string        `json:"resets_in,omitempty"`
}

// VendorWindows returns every window in the newest vendor reading for
// provider within the last two weeks, busiest first: Claude's 5-hour,
// weekly and model-scoped weekly windows from the Claude usage meter,
// Codex's primary and secondary windows. nil when there is none.
func VendorWindows(ctx context.Context, reader EventReader, provider eventschema.Provider, now time.Time) []VendorWindow {
	// Each vendor's windows come from its own reader; matching on keys
	// alone read Codex's primary window as Claude's.
	var (
		parse  func(map[string]string, time.Time) []VendorWindow
		source string
	)
	switch provider {
	case eventschema.ProviderAnthropic:
		parse, source = claudeWindows, "claude-usage-meter"
	case eventschema.ProviderOpenAI:
		parse, source = codexWindows, "codex-jsonl"
	default:
		return nil
	}
	events, err := reader.ReadEvents(ctx, eventschema.EventTypePrompt, now.Add(-14*24*time.Hour))
	if err != nil {
		return nil
	}
	var best *eventschema.Envelope
	for _, e := range events {
		if e == nil || e.Source != source || e.Attributes == nil || len(parse(e.Attributes, now)) == 0 {
			continue
		}
		if best == nil || e.Timestamp.After(best.Timestamp) {
			best = e
		}
	}
	if best == nil {
		return nil
	}
	out := parse(best.Attributes, now)
	sort.SliceStable(out, func(i, j int) bool { return out[i].UsedPct > out[j].UsedPct })
	return out
}

// claudeWindows reads the Claude usage meter's <label>_used_pct keys.
func claudeWindows(attrs map[string]string, now time.Time) []VendorWindow {
	var out []VendorWindow
	for key, raw := range attrs {
		label, ok := strings.CutSuffix(key, "_used_pct")
		if !ok {
			continue
		}
		pct, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		w := VendorWindow{UsedPct: pct}
		switch {
		case label == "five_hour":
			w.Name, w.Duration = "5h", 5*time.Hour
		case label == "seven_day" || strings.HasPrefix(label, "weekly") || strings.HasPrefix(label, "seven_day"):
			w.Name, w.Duration = "week", 7*24*time.Hour
		default:
			w.Name = strings.ReplaceAll(label, "_", " ")
		}
		if scope := attrs[label+"_model_scope"]; scope != "" {
			w.Name += " (" + scope + ")"
		} else if scope := attrs[label+"_surface_scope"]; scope != "" {
			w.Name += " (" + scope + ")"
		}
		setReset(&w, attrs[label+"_reset_at"], now)
		out = append(out, w)
	}
	return out
}

// codexWindows reads Codex's primary and secondary rate limits.
func codexWindows(attrs map[string]string, now time.Time) []VendorWindow {
	var out []VendorWindow
	for _, slot := range []string{"primary", "secondary"} {
		pct, err := strconv.ParseFloat(attrs[slot+"_used_pct"], 64)
		if err != nil {
			continue
		}
		w := VendorWindow{UsedPct: pct, Name: slot}
		minutes, err := strconv.Atoi(attrs[slot+"_window_min"])
		switch {
		case err == nil && minutes > 0:
			w.Duration = time.Duration(minutes) * time.Minute
			w.Name = windowWords(w.Duration)
		case err == nil:
			// A zero-length slot is a window the plan does not have
			// (a weekly-only plan reports an empty secondary).
			continue
		}
		setReset(&w, attrs[slot+"_resets_at"], now)
		out = append(out, w)
	}
	return out
}

func setReset(w *VendorWindow, raw string, now time.Time) {
	if raw == "" {
		return
	}
	if in := parseResetsIn(raw, now); in > 0 {
		w.ResetsAt = now.Add(in).UTC()
		w.ResetsIn = in.Round(time.Minute).String()
	}
}

// windowWords names a window length: "week", "day", "5h".
func windowWords(d time.Duration) string {
	switch {
	case d == 7*24*time.Hour:
		return "week"
	case d == 24*time.Hour:
		return "day"
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	default:
		return d.String()
	}
}
