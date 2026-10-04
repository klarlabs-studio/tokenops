// Package cards renders the glance (ADR 0010's one-call view) as terminal
// cards: one per plan, with a bar for every window the vendor reports, its
// reset, spend against a limit and credit left, laid out in a grid that
// fits the terminal. `tokenops glance` prints it; plain text, a brief
// table, or JSON are the alternatives.
package cards

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// Color is how much colour the terminal takes.
type Color int

const (
	// NoColor prints plain text: NO_COLOR, a pipe, --no-color.
	NoColor Color = iota
	// Basic uses the 16 ANSI colours.
	Basic
	// TrueColor uses the Klarlabs palette and gradient bars.
	TrueColor
)

// Options shapes the output.
type Options struct {
	// Width is the terminal's columns; 80 when not positive.
	Width int
	Color Color
	// Brief prints a compact table instead of cards.
	Brief bool
	Now   time.Time
}

// cardWidth is a card's outer width: two fit an 80-column terminal.
const cardWidth = 38

// Klarlabs palette (internal/brand/tokens.css), dark-mode roles.
type rgb struct{ r, g, b int }

var (
	cobaltFg  = rgb{0xA7, 0xB3, 0xFF}
	porcelain = rgb{0xDD, 0xE0, 0xE5}
	muted     = rgb{0x9D, 0xA2, 0xAC}
	line      = rgb{0x55, 0x5A, 0x64}
	okC       = rgb{0x86, 0xEF, 0xAC}
	warnC     = rgb{0xFC, 0xD3, 0x4D}
	dangerC   = rgb{0xF8, 0x71, 0x71}
)

// basic maps each palette colour to its nearest of the 16 ANSI colours.
var basic = map[rgb]int{cobaltFg: 94, porcelain: 97, muted: 37, line: 90, okC: 92, warnC: 93, dangerC: 91}

type painter Color

func (p painter) paint(c rgb, s string) string {
	switch Color(p) {
	case TrueColor:
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m", c.r, c.g, c.b, s)
	case Basic:
		return fmt.Sprintf("\x1b[%dm%s\x1b[0m", basic[c], s)
	}
	return s
}

func (p painter) bold(s string) string {
	if Color(p) == NoColor {
		return s
	}
	return "\x1b[1m" + s + "\x1b[0m"
}

// levelColor is green below 60%, amber below 80%, red above, as the
// statusline and the coach's quota tiers grade it.
func levelColor(pct float64) rgb {
	switch {
	case pct >= 80:
		return dangerC
	case pct >= 60:
		return warnC
	default:
		return okC
	}
}

// blend is the colour pct of the way along green → amber → red.
func blend(pct float64) rgb {
	mix := func(a, b rgb, t float64) rgb {
		return rgb{a.r + int(float64(b.r-a.r)*t), a.g + int(float64(b.g-a.g)*t), a.b + int(float64(b.b-a.b)*t)}
	}
	t := math.Max(0, math.Min(pct, 100)) / 100
	if t < 0.6 {
		return mix(okC, warnC, t/0.6)
	}
	return mix(warnC, dangerC, (t-0.6)/0.4)
}

// bar draws pct across width cells. In true colour each filled cell takes
// the gradient's colour at its own position, so a fuller bar reads hotter.
func (p painter) bar(pct float64, width int) string {
	filled := max(0, min(width, int(pct/100*float64(width)+0.5)))
	var b strings.Builder
	for i := range filled {
		c := levelColor(pct)
		if Color(p) == TrueColor {
			c = blend(float64(i+1) / float64(width) * 100)
		}
		b.WriteString(p.paint(c, "█"))
	}
	b.WriteString(p.paint(line, strings.Repeat("░", width-filled)))
	return b.String()
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// visible is s's width on screen.
func visible(s string) int { return utf8.RuneCountInString(ansi.ReplaceAllString(s, "")) }

// pad right-pads s to width on screen.
func pad(s string, width int) string {
	if n := visible(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// clip shortens plain s to width runes, with an ellipsis.
func clip(s string, width int) string {
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	r := []rune(s)
	return string(r[:max(0, width-1)]) + "…"
}

// humanReset words a Go duration as "6d 2h", "4h 23m" or "12m".
func humanReset(s string) string {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return ""
	}
	d = d.Round(time.Minute)
	days, hours, mins := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour), int(d%time.Hour/time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func money(usd float64) string {
	if usd >= 100 {
		return fmt.Sprintf("$%.0f", usd)
	}
	return fmt.Sprintf("$%.2f", usd)
}

// row is one measured line of a card.
type row struct {
	label  string
	pct    float64
	right  string // reset time or amounts
	hasPct bool
	text   string // a line without a bar
}

// rows lists what a plan's card shows, busiest window first.
func rows(r plans.HeadroomReport) []row {
	var out []row
	for _, w := range r.Windows {
		out = append(out, row{label: w.Name, pct: w.UsedPct, right: humanReset(w.ResetsIn), hasPct: true})
	}
	if len(r.Windows) == 0 && r.WindowCap > 0 {
		out = append(out, row{label: windowLabel(r.WindowDuration), pct: r.WindowPct, right: humanReset(r.WindowResetsIn), hasPct: true})
	}
	switch {
	case r.SpendLimitUSD > 0:
		out = append(out, row{label: "spend", pct: r.SpendPct, right: money(r.SpendUSD) + "/" + money(r.SpendLimitUSD), hasPct: true})
	case r.SpendUSD > 0:
		out = append(out, row{text: money(r.SpendUSD) + " this month, no limit known"})
	}
	if r.BalanceUSD != nil {
		out = append(out, row{text: money(*r.BalanceUSD) + " credit left"})
	}
	if len(out) == 0 && r.ConsumedTokens > 0 {
		out = append(out, row{text: tokens(r.ConsumedTokens) + " tokens this month"})
	}
	return out
}

func windowLabel(s string) string {
	d, _ := time.ParseDuration(s)
	switch {
	case d >= 7*24*time.Hour:
		return "week"
	case d > 0:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return "window"
}

func tokens(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.2fB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// card draws one plan.
func (p painter) card(r plans.HeadroomReport) []string {
	inner := cardWidth - 4
	risk := strings.ToUpper(r.OverageRisk)
	room := inner - 2 // no risk badge: title, a space, at least one dash
	if risk != "" {
		room = inner - len(risk) - 5
	}
	title := clip(r.Display, room)
	riskColor := map[string]rgb{"LOW": okC, "MEDIUM": warnC, "HIGH": dangerC}[risk]
	// ╭─ title ───── RISK ─╮ spans cardWidth: 3 + title + 1 + dashes + 1 + risk + 3.
	top := p.paint(line, "╭─ ") + p.bold(p.paint(porcelain, title)) + " " +
		p.paint(line, strings.Repeat("─", max(1, inner-visible(title)-len(risk)-4))) + " " +
		p.paint(riskColor, risk) + p.paint(line, " ─╮")
	if risk == "" {
		top = p.paint(line, "╭─ ") + p.bold(p.paint(porcelain, title)) + " " +
			p.paint(line, strings.Repeat("─", max(1, inner-visible(title)-2))+"─╮")
	}
	rs := rows(r)
	out := make([]string, 0, len(rs)+2)
	out = append(out, top)
	// The label column fits the card's longest label (up to 12) and the
	// right one its widest reset or amount (7 to 13); the bar takes the
	// rest.
	lw, rw7 := 4, 7
	for _, rw := range rs {
		if rw.hasPct {
			lw = max(lw, utf8.RuneCountInString(rw.label))
			rw7 = max(rw7, utf8.RuneCountInString(rw.right))
		}
	}
	lw, rw7 = min(lw, 12), min(rw7, 13)
	bw := max(4, inner-lw-1-1-4-1-rw7)
	for _, rw := range rs {
		var body string
		if rw.hasPct {
			label := pad(p.paint(muted, clip(rw.label, lw)), lw)
			pct := pad(p.paint(levelColor(rw.pct), fmt.Sprintf("%3.0f%%", rw.pct)), 4)
			body = label + " " + p.bar(rw.pct, bw) + " " + pct + " " + p.paint(muted, clip(rw.right, inner-lw-bw-7+1))
		} else {
			body = p.paint(muted, clip(rw.text, inner))
		}
		out = append(out, p.paint(line, "│ ")+pad(body, inner)+p.paint(line, " │"))
	}
	source := ""
	if r.SignalQuality.Source != "" {
		source = " " + strings.ReplaceAll(r.SignalQuality.Source, "_", " ") + " "
	}
	source = clip(source, inner-2)
	out = append(out, p.paint(line, "╰"+strings.Repeat("─", cardWidth-3-utf8.RuneCountInString(source)))+p.paint(muted, source)+p.paint(line, "─╯"))
	return out
}

// Render draws the glance.
func Render(g headroom.Glance, opt Options) string {
	if opt.Width <= 0 {
		opt.Width = 80
	}
	p := painter(opt.Color)
	if opt.Brief {
		return brief(g, p)
	}
	var b strings.Builder
	b.WriteString(p.bold(p.paint(cobaltFg, "TokenOps")))
	if s := g.Insight.Summary; s != "" {
		b.WriteString(p.paint(line, "  ·  ") + p.paint(porcelain, clip(s, max(20, opt.Width-14))))
	}
	b.WriteString("\n\n")
	switch {
	case g.Headroom.Unconfigured != "":
		b.WriteString(p.paint(warnC, g.Headroom.Unconfigured) + "\n")
		return b.String()
	case g.Headroom.StorageDisabled != "":
		b.WriteString(p.paint(warnC, g.Headroom.StorageDisabled) + "\n")
		return b.String()
	}
	reports := append([]plans.HeadroomReport(nil), g.Headroom.Reports...)
	sort.SliceStable(reports, func(i, j int) bool { return busiest(reports[i]) > busiest(reports[j]) })
	perRow := max(1, (opt.Width+2)/(cardWidth+2))
	for i := 0; i < len(reports); i += perRow {
		group := reports[i:min(i+perRow, len(reports))]
		drawn := make([][]string, len(group))
		height := 0
		for j, r := range group {
			drawn[j] = p.card(r)
			height = max(height, len(drawn[j]))
		}
		for at := range height {
			var parts []string
			for _, c := range drawn {
				switch {
				case at < len(c)-1:
					parts = append(parts, pad(c[at], cardWidth))
				case at == height-1:
					parts = append(parts, pad(c[len(c)-1], cardWidth))
				default:
					// A shorter card is padded to its row's height.
					parts = append(parts, p.paint(line, "│")+strings.Repeat(" ", cardWidth-2)+p.paint(line, "│"))
				}
			}
			b.WriteString(strings.TrimRight(strings.Join(parts, "  "), " ") + "\n")
		}
	}
	for _, n := range g.Headroom.Notes {
		b.WriteString(p.paint(warnC, "! ") + p.paint(muted, n) + "\n")
	}
	return b.String()
}

// busiest is a plan's fullest measure, to order the cards.
func busiest(r plans.HeadroomReport) float64 {
	m := r.SpendPct
	for _, w := range r.Windows {
		m = math.Max(m, w.UsedPct)
	}
	return math.Max(m, r.WindowPct)
}

// brief is one line per measure: plan, window, share, reset.
func brief(g headroom.Glance, p painter) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-26s %-14s %5s  %s\n", "PLAN", "WINDOW", "USED", "RESETS")
	for _, r := range g.Headroom.Reports {
		for _, rw := range rows(r) {
			if !rw.hasPct {
				fmt.Fprintf(&b, "%-26s %s\n", clip(r.Display, 26), p.paint(muted, rw.text))
				continue
			}
			used := p.paint(levelColor(rw.pct), fmt.Sprintf("%4.0f%%", rw.pct))
			fmt.Fprintf(&b, "%-26s %-14s %s  %s\n", clip(r.Display, 26), clip(rw.label, 14), used, rw.right)
		}
	}
	return b.String()
}
