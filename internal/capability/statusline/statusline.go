// Package statusline renders TokenOps' line under an agent's prompt: the
// quota windows, where the session's context stands against the point it
// compacts at, its cache, its cost in the operator's currency, and the
// coach's open tip. It is pure: the caller gathers the facts from what
// the agent hands over and from files TokenOps already keeps, because a
// statusline runs on every turn and must never wait on a database or the
// network.
//
// Colours follow the Klarlabs brand ("Studio Grotesk": cool porcelain
// neutrals, a single cobalt accent), in its dark-mode roles, since agent
// terminals are mostly dark.
package statusline

import (
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/biller"
	"go.klarlabs.de/tokenops/internal/contexts/spend/fx"
)

// Window is one usage window: the 5-hour or weekly quota, or a spend
// limit.
type Window struct {
	Label   string
	UsedPct float64
	// ResetsAt is when the window resets; zero when unknown.
	ResetsAt time.Time
}

// Input is everything the line shows.
type Input struct {
	// Model and Effort name what is answering ("Opus 5.5", "high").
	Model, Effort string
	// ContextPct is how full the context window is, 0–100.
	ContextPct float64
	// CompactPct is where the session compacts, as a share of the window,
	// 0 when unknown.
	CompactPct float64
	// CacheHit is the prompt cache's hit ratio, 0–1; negative when
	// unknown.
	CacheHit float64
	// CostUSD is the session's cost at list price, as the agent reports
	// it. On a subscription it is value, not money spent.
	CostUSD float64
	// Covered says a plan covers the session, so the cost is value.
	Covered bool
	Windows []Window
	// Rate converts dollars into the operator's currency.
	Rate fx.Rate
	// Tip is the coach's open tip, already worded.
	Tip string
	Now time.Time
	// Color is false for NO_COLOR and dumb terminals.
	Color bool
}

// Klarlabs brand colours, dark-mode roles (internal/brand/tokens.css).
var (
	cobalt    = rgb(0x54, 0x68, 0xF8) // blue-400, bars
	cobaltFg  = rgb(0xA7, 0xB3, 0xFF) // blue-250, accent text
	porcelain = rgb(0xDD, 0xE0, 0xE5) // ink-200, figures
	muted     = rgb(0x9D, 0xA2, 0xAC) // ink-400, labels
	line      = rgb(0x55, 0x5A, 0x64) // ink-600, separators and empty cells
	okFg      = rgb(0x86, 0xEF, 0xAC) // green-300
	warnFg    = rgb(0xFC, 0xD3, 0x4D) // amber-300
	dangerFg  = rgb(0xF8, 0x71, 0x71) // red-400
)

func rgb(r, g, b int) string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b) }

const reset = "\x1b[0m"

// painter colours text, or leaves it plain.
type painter bool

func (p painter) paint(color, s string) string {
	if !p || s == "" {
		return s
	}
	return color + s + reset
}

// level colours a usage percentage by how close it is to the limit,
// with the same thresholds the coach's quota tiers use.
func level(pct float64) string {
	switch {
	case pct >= 80:
		return dangerFg
	case pct >= 60:
		return warnFg
	default:
		return okFg
	}
}

// bar draws pct as cells of a fixed width.
func (p painter) bar(pct float64, width int) string {
	filled := int(pct/100*float64(width) + 0.5)
	filled = max(0, min(width, filled))
	return p.paint(level(pct), strings.Repeat("▰", filled)) + p.paint(line, strings.Repeat("▱", width-filled))
}

// Render returns the line, and a second one when the coach has a tip.
func Render(in Input) []string {
	p := painter(in.Color)
	sep := p.paint(line, " · ")
	var parts []string

	if in.Model != "" {
		m := p.paint(cobaltFg, in.Model)
		if in.Effort != "" {
			m += p.paint(muted, " "+in.Effort)
		}
		parts = append(parts, m)
	}
	if w, ok := tightest(in.Windows); ok {
		s := p.bar(w.UsedPct, 6) + " " + window(p, w, in.Now)
		for _, o := range in.Windows {
			if o.Label != w.Label {
				s += sep + window(p, o, in.Now)
			}
		}
		parts = append(parts, s)
	}
	if in.ContextPct > 0 {
		s := p.paint(muted, "ctx ") + p.paint(level(contextLevel(in)), fmt.Sprintf("%.0f%%", in.ContextPct))
		if in.CompactPct > 0 {
			s += p.paint(muted, fmt.Sprintf(" → %.0f%%", in.CompactPct))
		}
		parts = append(parts, s)
	}
	if in.CacheHit >= 0 {
		parts = append(parts, p.paint(muted, "cache ")+p.paint(porcelain, fmt.Sprintf("%.0f%%", in.CacheHit*100)))
	}
	if in.CostUSD > 0 {
		cost := money(in.Rate, in.CostUSD)
		label := ""
		if in.Covered {
			// A plan covers it: the figure is what the work was worth at
			// list price, not what was paid.
			label = p.paint(muted, " value")
		}
		parts = append(parts, p.paint(porcelain, cost)+label)
	}
	out := []string{strings.Join(parts, sep)}
	if in.Tip != "" {
		out = append(out, p.paint(cobalt, "●")+" "+p.paint(porcelain, in.Tip))
	}
	return out
}

// contextLevel is the context fill measured against where the session
// compacts, so the colour warns before compaction rather than at 100%.
func contextLevel(in Input) float64 {
	if in.CompactPct > 0 {
		return in.ContextPct / in.CompactPct * 100
	}
	return in.ContextPct
}

// tightest is the window closest to its limit.
func tightest(ws []Window) (Window, bool) {
	var best Window
	found := false
	for _, w := range ws {
		if !found || w.UsedPct > best.UsedPct {
			best, found = w, true
		}
	}
	return best, found
}

func window(p painter, w Window, now time.Time) string {
	s := p.paint(muted, w.Label+" ") + p.paint(level(w.UsedPct), fmt.Sprintf("%.0f%%", w.UsedPct))
	if !w.ResetsAt.IsZero() && w.ResetsAt.After(now) {
		s += p.paint(muted, " ↻"+resetIn(w.ResetsAt, now))
	}
	return s
}

// resetIn says when a window resets: a clock time today, else a day.
func resetIn(at, now time.Time) string {
	at = at.In(now.Location())
	if d := at.Sub(now); d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes())+1)
	}
	if at.YearDay() == now.YearDay() && at.Year() == now.Year() {
		return at.Format("15:04")
	}
	return at.Format("Mon")
}

// money shows a dollar amount in the operator's currency.
func money(r fx.Rate, usd float64) string {
	if r.Valid() && !r.IsUSD() {
		return fmt.Sprintf("%s%.2f", symbol(r.Currency), r.FromUSD(usd))
	}
	return fmt.Sprintf("$%.2f", usd)
}

func symbol(currency string) string {
	switch strings.ToUpper(currency) {
	case "EUR":
		return "€"
	case "GBP":
		return "£"
	case "JPY":
		return "¥"
	case "CHF":
		return "CHF "
	default:
		return strings.ToUpper(currency) + " "
	}
}

// Subagent is one subagent row under Claude Code's prompt.
type Subagent struct {
	Name, Model, Effort string
	// ContextPct is how full its context window is, 0–100; negative when
	// unknown.
	ContextPct float64
	Color      bool
}

// SubagentRow renders one subagent's row: which model it runs on, at
// what effort, and how full its context is. The coach's models power
// moves subagents to cheaper models, so this is where that shows.
func SubagentRow(s Subagent) string {
	p := painter(s.Color)
	parts := []string{p.paint(cobaltFg, s.Name)}
	if m := shortModel(s.Model); m != "" {
		mt := p.paint(porcelain, m)
		if s.Effort != "" {
			mt += p.paint(muted, " "+s.Effort)
		}
		parts = append(parts, mt)
	}
	if s.ContextPct >= 0 {
		parts = append(parts, p.bar(s.ContextPct, 4)+" "+p.paint(level(s.ContextPct), fmt.Sprintf("%.0f%%", s.ContextPct)))
	}
	return strings.Join(parts, p.paint(line, " · "))
}

// shortModel drops the vendor prefix and a date suffix from a model ID:
// claude-haiku-4-5-20251001 reads as haiku-4-5.
func shortModel(id string) string {
	id = strings.TrimPrefix(id, "claude-")
	if i := strings.LastIndex(id, "-"); i > 0 && len(id)-i-1 == 8 && strings.Trim(id[i+1:], "0123456789") == "" {
		id = id[:i]
	}
	return id
}

// Covered reports whether a plan covers a Claude Code session: a plan is
// bound to Anthropic (planCovers) and Claude Code goes through Anthropic's
// own endpoint, since a plan covers nothing a gateway carried (ADR 0009).
func Covered(planCovers bool, baseURL string) bool {
	return planCovers && biller.PlanApplies("anthropic", biller.EndpointName(baseURL, "anthropic"))
}
