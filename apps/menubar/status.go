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
	Name     string  `json:"name"`
	UsedPct  float64 `json:"used_pct"`
	ResetsIn string  `json:"resets_in"`
}

// status is what the tray shows: the share the icon's ring fills, and a
// tooltip with a line per window. Title names the busiest window.
type status struct {
	Title   string
	Pct     float64
	Tooltip string
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

// statusOf picks the busiest measure across every plan: a vendor window,
// else spend against a limit. That is the one that stops work first.
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
	consider := func(name string, pct float64, line string) {
		lines = append(lines, line)
		if !found || pct > best.Pct {
			best, found = status{Title: fmt.Sprintf("%s %.0f%%", name, pct), Pct: pct}, true
		}
	}
	for _, r := range g.PlanHeadroom.Reports {
		name := shortName(r.Provider)
		for _, w := range r.Windows {
			line := fmt.Sprintf("%s · %s %.0f%%", name, w.Name, w.UsedPct)
			if w.ResetsIn != "" {
				line += " · resets in " + humanDuration(w.ResetsIn)
			}
			consider(name, w.UsedPct, line)
		}
		if len(r.Windows) == 0 && r.SpendLimitUSD > 0 {
			consider(name, r.SpendPct, fmt.Sprintf("%s · $%.2f of $%.2f", name, r.SpendUSD, r.SpendLimitUSD))
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

// trayIcon is a ring filled clockwise to pct, as a template image (black
// with alpha, tinted by macOS to suit the menu bar).
func trayIcon(pct float64) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	c := float64(iconSize) / 2
	outer, inner := c-1, c-6
	used := math.Max(0, math.Min(pct, 100)) / 100
	for y := range iconSize {
		for x := range iconSize {
			dx, dy := float64(x)+0.5-c, float64(y)+0.5-c
			r := math.Hypot(dx, dy)
			if r > outer {
				continue
			}
			a := math.Atan2(dx, -dy) / (2 * math.Pi)
			if a < 0 {
				a++
			}
			if a < used || r >= inner {
				img.SetNRGBA(x, y, color.NRGBA{A: 0xff})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
