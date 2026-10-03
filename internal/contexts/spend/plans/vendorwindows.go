package plans

import (
	"context"
	"maps"
	"slices"
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
		parse   func(map[string]string, time.Time) []VendorWindow
		sources []string
	)
	switch provider {
	case eventschema.ProviderAnthropic:
		// The claude.ai meter and Claude Code's status line report the
		// same windows in the same shape.
		parse, sources = claudeWindows, ClaudeWindowSources
	case eventschema.ProviderOpenAI:
		parse, sources = codexWindows, []string{"codex-jsonl"}
	default:
		return accountWindows(ctx, reader, provider, now)
	}
	events, err := reader.ReadEvents(ctx, eventschema.EventTypePrompt, now.Add(-14*24*time.Hour))
	if err != nil {
		return nil
	}
	newest := map[string]*eventschema.Envelope{}
	for _, e := range events {
		if e == nil || !slices.Contains(sources, e.Source) || e.Attributes == nil || len(parse(e.Attributes, now)) == 0 {
			continue
		}
		if best := newest[e.Source]; best == nil || e.Timestamp.After(best.Timestamp) {
			newest[e.Source] = e
		}
	}
	if len(newest) == 0 {
		return nil
	}
	out := parse(MergeReadings(newest), now)
	sort.SliceStable(out, func(i, j int) bool { return out[i].UsedPct > out[j].UsedPct })
	return out
}

// ClaudeWindowSources are the sources that report Claude's plan windows:
// the claude.ai usage meter and Claude Code's status line.
var ClaudeWindowSources = []string{"claude-usage-meter", "claude-code-statusline"}

// MergeReadings overlays the newest reading of each source, oldest first,
// so where two sources report the same window the newer figure wins, and
// a window only one of them reports (the meter's model-scoped weeks) is
// kept.
func MergeReadings(newest map[string]*eventschema.Envelope) map[string]string {
	readings := make([]*eventschema.Envelope, 0, len(newest))
	for _, e := range newest {
		readings = append(readings, e)
	}
	sort.Slice(readings, func(i, j int) bool { return readings[i].Timestamp.Before(readings[j].Timestamp) })
	merged := map[string]string{}
	for _, e := range readings {
		maps.Copy(merged, e.Attributes)
	}
	return merged
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
		case label == "spend_limit":
			w.Name, w.Duration = spendLimitWindow(attrs["spend_limit_period"])
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

// accountWindows reads the windows a vendor account reader stored as
// window_<n>_{name,used_pct,duration_min,reset_at}: the newest such
// reading for provider in the last two weeks, busiest first.
func accountWindows(ctx context.Context, reader EventReader, provider eventschema.Provider, now time.Time) []VendorWindow {
	events, err := reader.ReadEvents(ctx, eventschema.EventTypePrompt, now.Add(-14*24*time.Hour))
	if err != nil {
		return nil
	}
	var best *eventschema.Envelope
	for _, e := range events {
		if e == nil || e.Attributes["window_0_used_pct"] == "" || readingProvider(e) != provider {
			continue
		}
		if best == nil || e.Timestamp.After(best.Timestamp) {
			best = e
		}
	}
	if best == nil {
		return nil
	}
	var out []VendorWindow
	for i := 0; ; i++ {
		k := "window_" + strconv.Itoa(i) + "_"
		raw, ok := best.Attributes[k+"used_pct"]
		if !ok {
			break
		}
		pct, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		w := VendorWindow{Name: best.Attributes[k+"name"], UsedPct: pct}
		if m, err := strconv.Atoi(best.Attributes[k+"duration_min"]); err == nil && m > 0 {
			w.Duration = time.Duration(m) * time.Minute
		}
		setReset(&w, best.Attributes[k+"reset_at"], now)
		out = append(out, w)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UsedPct > out[j].UsedPct })
	return out
}

// SubscriptionReadingProviders lists providers whose own account reader
// reported a subscription's windows in the last two weeks. Headroom binds
// such a provider to Subscription when no plan is bound: the vendor has
// said it is on a plan, and shown how much of it is used.
func SubscriptionReadingProviders(ctx context.Context, reader EventReader, now time.Time) []string {
	events, err := reader.ReadEvents(ctx, eventschema.EventTypePrompt, now.Add(-14*24*time.Hour))
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range events {
		if e == nil || e.Attributes["billing"] != "subscription" || e.Attributes["window_0_used_pct"] == "" {
			continue
		}
		if p := string(readingProvider(e)); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
