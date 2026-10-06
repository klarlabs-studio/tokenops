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
}

type window struct {
	Name        string  `json:"name"`
	VendorLabel string  `json:"vendor_label"`
	UsedPct     float64 `json:"used_pct"`
	ResetsIn    string  `json:"resets_in"`
	Pace        *pace   `json:"pace"`
	Stale       bool    `json:"stale"`
}

// pace is the daemon's verdict on a window (plans.WindowPace).
type pace struct {
	Status       string `json:"status"`
	LastsToReset bool   `json:"lasts_to_reset"`
	RunsOutInNs  int64  `json:"runs_out_in_ns"`
}

// status is what the tray shows. The icon's two rings are the busiest
// plan's windows, each filled to the share left: the outer ring its
// longest window (the week), the inner its session. Title names the
// busiest window; the tooltip has a line per window.
type status struct {
	Title string
	// Pct is the busiest window's share used, across every plan.
	Pct float64
	// Outer and Inner are the busiest plan's windows, as shares used.
	// Inner is negative when the plan has no session window.
	Outer, Inner float64
	Tooltip      string
}

// providerNames are short names for the menu bar.
var providerNames = map[string]string{
	"anthropic": "Claude", "openai": "Codex", "gemini": "Gemini", "github": "Copilot",
	"cursor": "Cursor", "fireworks": "Fireworks", "openrouter": "OpenRouter",
}

func shortName(provider string) string {
	if n, ok := providerNames[provider]; ok {
		return n
	}
	if provider == "" {
		return "?"
	}
	return strings.ToUpper(provider[:1]) + provider[1:]
}

// sessionWindow is the short rolling window vendors call the session.
const sessionWindow = "5h"

// left is the share of a window still available.
func left(used float64) float64 { return math.Max(0, math.Min(100, 100-used)) }

// statusOf picks the busiest measure across every plan: a vendor window,
// else spend against a limit. That is the one that stops work first, and
// its plan is the one the icon shows.
func statusOf(raw json.RawMessage) status {
	var g glanceView
	if json.Unmarshal(raw, &g) != nil {
		return status{Title: "—", Inner: -1, Tooltip: "TokenOps: unreadable answer from the daemon"}
	}
	if g.PlanHeadroom.Error != "" {
		return status{Title: "—", Inner: -1, Tooltip: "TokenOps: " + g.PlanHeadroom.Hint}
	}
	var (
		best  status
		found bool
		lines []string
	)
	for _, r := range g.PlanHeadroom.Reports {
		name := shortName(r.Provider)
		rs := status{Inner: -1, Outer: -1}
		for _, w := range r.Windows {
			line := fmt.Sprintf("%s · %s %.0f%% left", name, w.Name, left(w.UsedPct))
			if w.ResetsIn != "" {
				line += " · resets in " + humanDuration(w.ResetsIn)
			}
			lines = append(lines, line)
			if w.Name == sessionWindow {
				rs.Inner = w.UsedPct
			} else {
				rs.Outer = math.Max(rs.Outer, w.UsedPct)
			}
			if w.UsedPct >= rs.Pct {
				rs.Pct, rs.Title = w.UsedPct, fmt.Sprintf("%s %.0f%% left", name, left(w.UsedPct))
			}
		}
		if len(r.Windows) == 0 && r.SpendLimitUSD > 0 {
			lines = append(lines, fmt.Sprintf("%s · $%.2f of $%.2f", name, r.SpendUSD, r.SpendLimitUSD))
			rs.Outer, rs.Pct, rs.Title = r.SpendPct, r.SpendPct, fmt.Sprintf("%s %.0f%% left", name, left(r.SpendPct))
		}
		if rs.Title == "" {
			continue
		}
		if rs.Outer < 0 {
			// A session window alone is drawn as the one ring.
			rs.Outer, rs.Inner = rs.Inner, -1
		}
		if !found || rs.Pct > best.Pct {
			best, found = rs, true
		}
	}
	if !found {
		return status{Title: "TokenOps", Inner: -1, Tooltip: "TokenOps: " + g.Insight.Summary}
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

// Ring bands, in pixels from the centre: the outer ring is the long
// window, the inner the session.
const (
	outerFrom, outerTo = 12.6, 16.8
	innerFrom, innerTo = 5.6, 9.8
	trackAlpha         = 0x55
	iconSamples        = 4
)

// ringAlpha is the opacity at a point: solid where a ring is filled, faint
// on its track, clear elsewhere.
func ringAlpha(px, py, outerUsed, innerUsed float64) float64 {
	c := float64(iconSize) / 2
	dx, dy := px-c, py-c
	r := math.Hypot(dx, dy)
	used := outerUsed
	switch {
	case r >= outerFrom && r <= outerTo:
	case r >= innerFrom && r <= innerTo && innerUsed >= 0:
		used = innerUsed
	default:
		return 0
	}
	a := math.Atan2(dx, -dy) / (2 * math.Pi)
	if a < 0 {
		a++
	}
	if used >= 0 && a < left(used)/100 {
		return 1
	}
	return float64(trackAlpha) / 0xff
}

// trayIcon draws two rings, each filled clockwise from the top to the
// share left of its window over a faint track, as a template image (black
// with alpha, tinted by macOS to suit the menu bar). A negative used
// leaves that ring a bare track; a negative inner draws no inner ring.
func trayIcon(outerUsed, innerUsed float64) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	for y := range iconSize {
		for x := range iconSize {
			// Each pixel is the mean of a grid of samples, so the rings'
			// edges are smooth rather than stepped.
			var sum float64
			for sy := range iconSamples {
				for sx := range iconSamples {
					sum += ringAlpha(float64(x)+(float64(sx)+0.5)/iconSamples, float64(y)+(float64(sy)+0.5)/iconSamples, outerUsed, innerUsed)
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
