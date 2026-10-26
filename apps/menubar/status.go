package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"time"
)

// glanceView is the part of GET /api/glance the tray reads. The panel gets
// the whole answer.
type glanceView struct {
	Insight struct {
		Level   string `json:"level"`
		Summary string `json:"summary"`
	} `json:"insight"`
	PlanHeadroom struct {
		Reports []report `json:"reports"`
		Error   string   `json:"error"`
		Hint    string   `json:"hint"`
	} `json:"plan_headroom"`
}

type report struct {
	Provider      string   `json:"provider"`
	Display       string   `json:"display"`
	OverageRisk   string   `json:"overage_risk"`
	Windows       []window `json:"windows"`
	SpendUSD      float64  `json:"spend_usd"`
	SpendLimitUSD float64  `json:"spend_limit_usd"`
	SpendPct      float64  `json:"spend_pct"`
	// SpendScope is "account" when the spend is the whole account's
	// (every member together); SpendScopeLabel names it for display.
	SpendScope      string `json:"spend_scope"`
	SpendScopeLabel string `json:"spend_scope_label"`
}

type window struct {
	Name        string  `json:"name"`
	VendorLabel string  `json:"vendor_label"`
	UsedPct     float64 `json:"used_pct"`
	ResetsIn    string  `json:"resets_in"`
	// NotStarted is a window no request has started yet: it has no reset
	// to count down to, and starts at the next request.
	NotStarted bool  `json:"not_started"`
	Pace       *pace `json:"pace"`
	Stale      bool  `json:"stale"`
}

// pace is the daemon's verdict on a window (plans.WindowPace).
type pace struct {
	Status       string `json:"status"`
	LastsToReset bool   `json:"lasts_to_reset"`
	RunsOutInNs  int64  `json:"runs_out_in_ns"`
}

// status is what the tray shows. The icon is a fuel gauge: the headroom
// ring, filled to the share left of the tightest window across every plan
// (see gaugeLevel). Title names that window; the tooltip has a line per
// window.
type status struct {
	Title string
	// Pct is the tightest window's share used, across every plan.
	Pct float64
	// Known is true when Pct is a live reading. The zero status is unknown,
	// so a path that forgets to set it shows the unknown gauge, never a full
	// or an empty one.
	Known bool
	// Stale marks that the tightest window's reading is out of date.
	Stale   bool
	Tooltip string
}

// providerNames (providers_gen.go) are generated from TokenOps' provider
// registry; this module cannot import it.

func shortName(provider string) string {
	if n, ok := providerNames[provider]; ok {
		return n
	}
	if provider == "" {
		return "?"
	}
	return strings.ToUpper(provider[:1]) + provider[1:]
}

// left is the share of a window still available.
func left(used float64) float64 { return math.Max(0, math.Min(100, 100-used)) }

// statusOf picks the busiest measure across every plan: a vendor window,
// else spend against a limit. That is the one that stops work first, and
// its plan is the one the icon shows.
func statusOf(raw json.RawMessage) status {
	var g glanceView
	if json.Unmarshal(raw, &g) != nil {
		return status{Title: "—", Tooltip: "TokenOps: unreadable answer from the daemon"}
	}
	if g.PlanHeadroom.Error != "" {
		return status{Title: "—", Tooltip: "TokenOps: " + g.PlanHeadroom.Hint}
	}
	var (
		best  status
		found bool
		lines []string
	)
	for _, r := range g.PlanHeadroom.Reports {
		name := shortName(r.Provider)
		rs := status{}
		for _, w := range r.Windows {
			line := fmt.Sprintf("%s · %s %.0f%% left", name, w.Name, left(w.UsedPct))
			switch {
			case w.NotStarted:
				line += " · starts at your next request"
			case w.ResetsIn != "":
				line += " · resets in " + humanDuration(w.ResetsIn)
			}
			lines = append(lines, line)
			if w.UsedPct >= rs.Pct {
				rs.Pct, rs.Stale = w.UsedPct, w.Stale
				rs.Title = fmt.Sprintf("%s %.0f%% left", name, left(w.UsedPct))
			}
		}
		if len(r.Windows) == 0 && r.SpendLimitUSD > 0 {
			lines = append(lines, fmt.Sprintf("%s · $%.2f of $%.2f", name, r.SpendUSD, r.SpendLimitUSD))
			rs.Pct, rs.Title = r.SpendPct, fmt.Sprintf("%s %.0f%% left", name, left(r.SpendPct))
		}
		if rs.Title == "" {
			continue
		}
		rs.Known = true
		if !found || rs.Pct > best.Pct {
			best, found = rs, true
		}
	}
	if !found {
		return status{Title: "TokenOps", Tooltip: "TokenOps: " + g.Insight.Summary}
	}
	best.Tooltip = strings.Join(lines, "\n")
	return best
}

// humanDuration words a Go duration string as the menu bar shows it:
// "146h45m0s" is "6d 2h", "4h25m0s" is "4h 25m", "12m30s" is "12m".
func humanDuration(s string) string {
	d, err := time.ParseDuration(s)
	if err != nil {
		return s
	}
	d = d.Round(time.Minute)
	days, hours, mins := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour), int(d%time.Hour/time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// iconSize is the tray icon's edge in pixels: 18 pt at 2x.
const iconSize = 36

// The gauge is the headroom ring (the brand mark's o): an outline that is
// always drawn, and inside it a tank that fills from the bottom.
const (
	ringFrom, ringTo = 14.2, 16.8
	tankRadius       = 11.6
	dotRadius        = 3.0
	iconSamples      = 4
	// gaugeSteps is how many fill levels the tank has above empty, so the
	// icon moves in visible steps and does not redraw on every percent.
	gaugeSteps = 8
	// gaugeUnknown is the level of a missing or stale reading.
	gaugeUnknown = -1
)

// gaugeLevel is the tank's fill, 0 (empty) to gaugeSteps (full), or
// gaugeUnknown. Empty means used up and nothing else: any headroom left
// rounds up to at least one step, and full means 100% left, so 99% left
// is one step short. A stale or unread figure is never full or empty.
func gaugeLevel(st status) int {
	if !st.Known || st.Stale || math.IsNaN(st.Pct) {
		return gaugeUnknown
	}
	l := left(st.Pct)
	switch {
	case l <= 0:
		return 0
	case l >= 100:
		return gaugeSteps
	}
	return min(gaugeSteps-1, max(1, int(math.Ceil(l/100*gaugeSteps))))
}

// gaugeAlpha is the opacity at a point: solid on the outline and in the
// filled part of the tank, and the centre dot of the unknown state.
func gaugeAlpha(px, py float64, level int) float64 {
	c := float64(iconSize) / 2
	dx, dy := px-c, py-c
	r := math.Hypot(dx, dy)
	switch {
	case r >= ringFrom && r <= ringTo:
		return 1
	case level == gaugeUnknown:
		if r <= dotRadius {
			return 1
		}
	case r <= tankRadius && dy >= tankRadius-2*tankRadius*float64(level)/gaugeSteps:
		return 1
	}
	return 0
}

// trayIcon draws the gauge as a template image (black with alpha, tinted
// by macOS to suit a light or dark menu bar).
func trayIcon(level int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	for y := range iconSize {
		for x := range iconSize {
			// Each pixel is the mean of a grid of samples, so the edges are
			// smooth rather than stepped.
			var sum float64
			for sy := range iconSamples {
				for sx := range iconSamples {
					sum += gaugeAlpha(float64(x)+(float64(sx)+0.5)/iconSamples, float64(y)+(float64(sy)+0.5)/iconSamples, level)
				}
			}
			if a := sum / (iconSamples * iconSamples); a > 0 {
				img.SetNRGBA(x, y, color.NRGBA{A: uint8(math.Round(a * 0xff))})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
