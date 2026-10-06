// Package cards renders the glance (ADR 0010's one-call view) as terminal
// cards, laid out as CodexBar's `codexbar cards` lays out its own: one card
// per plan with its source and plan, then for every window the vendor
// reports its share used, a bar, the reset and the pace, then spend, credit
// and cost. The grid fits the terminal. `tokenops glance` prints it; a
// brief table or JSON are the alternatives.
package cards

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/spending"
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
	// Costs is each provider's usage today and over 30 days, keyed by
	// provider; a plan without an entry shows no cost.
	Costs map[string]spending.ProviderCost
	// Findings are what the coach and the session analysis observed;
	// nil leaves the coach section out.
	Findings *findings.Report
	// CostsLate marks cost lookups cut off before they finished; a plan
	// without its cost then says so instead of leaving it out silently.
	CostsLate bool
	// OnlyFindings prints the coach section alone, every finding in it.
	OnlyFindings bool
}

// maxFindings is how many findings the cards show; the rest are counted.
const maxFindings = 6

// Cards are 38 to 42 wide with two columns between them, as CodexBar's:
// two fit 80 columns, and they stretch to fill wider rows.
const (
	minCardWidth = 38
	maxCardWidth = 42
	cardGap      = 2
)

// Klarlabs palette (internal/brand/tokens.css), dark-mode roles.
type rgb struct{ r, g, b int }

var (
	cobaltFg  = rgb{0xA7, 0xB3, 0xFF}
	porcelain = rgb{0xDD, 0xE0, 0xE5}
	muted     = rgb{0x9D, 0xA2, 0xAC}
	line      = rgb{0x55, 0x5A, 0x64}
	track     = rgb{0x1B, 0x1F, 0x3A} // an empty bar cell: ink tinted cobalt
	okC       = rgb{0x86, 0xEF, 0xAC}
	warnC     = rgb{0xFC, 0xD3, 0x4D}
	dangerC   = rgb{0xF8, 0x71, 0x71}
)

// basic maps each palette colour to its nearest of the 16 ANSI colours.
var basic = map[rgb]int{cobaltFg: 94, porcelain: 97, muted: 37, line: 90, track: 90, okC: 92, warnC: 93, dangerC: 91}

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

// bar draws the share of a window left across width cells, coloured by
// the share used (pct), so a shorter bar reads hotter. In true colour the
// cells are solid blocks of background colour; elsewhere a heavy rule for
// what is left and a light one for what is used.
func (p painter) bar(pct float64, width int) string {
	remaining := 100 - pct
	filled := max(0, min(width, int(remaining/100*float64(width)+0.5)))
	if remaining > 0 && filled == 0 {
		filled = 1 // a window with anything left never looks empty
	}
	var b strings.Builder
	switch Color(p) {
	case TrueColor:
		for i := range width {
			c := track
			if i < filled {
				c = blend(pct)
			}
			fmt.Fprintf(&b, "\x1b[48;2;%d;%d;%dm \x1b[0m", c.r, c.g, c.b)
		}
	default:
		b.WriteString(p.paint(levelColor(pct), strings.Repeat("━", filled)))
		b.WriteString(p.paint(line, strings.Repeat("─", width-filled)))
	}
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

// money is "$4.20", or "$10,948" from $100 up.
func money(usd float64) string {
	if usd < 100 {
		return fmt.Sprintf("$%.2f", usd)
	}
	digits := fmt.Sprintf("%.0f", usd)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return "$" + b.String()
}

// humanDuration words a duration as humanReset does.
func humanDuration(d time.Duration) string { return humanReset(d.String()) }

// vendors names each provider as its users do.
var vendors = map[string]string{
	"anthropic": "Claude", "openai": "Codex", "gemini": "Gemini", "github": "Copilot", "cursor": "Cursor",
	"fireworks": "Fireworks", "openrouter": "OpenRouter", "deepseek": "DeepSeek", "moonshot": "Moonshot",
	"zai": "z.ai", "minimax": "MiniMax", "mistral": "Mistral", "xai": "xAI",
}

// vendor is the card's title.
func vendor(r plans.HeadroomReport) string {
	if v, ok := vendors[r.Provider]; ok {
		return v
	}
	if r.Provider == "" {
		return r.Display
	}
	return strings.ToUpper(r.Provider[:1]) + r.Provider[1:]
}

// planName is the plan without its vendor's name: "Max 20x", "Pro
// Standard ($100)".
func planName(r plans.HeadroomReport) string {
	d := r.Display
	for _, prefix := range []string{"Claude ", "ChatGPT ", "Gemini ", "GitHub Copilot ", "Cursor "} {
		d = strings.TrimPrefix(d, prefix)
	}
	return d
}

// sourceBadge is where the reading came from, in a word.
func sourceBadge(source string) string {
	s := strings.ToLower(strings.ReplaceAll(source, "_", " "))
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "meter"):
		return "meter"
	case strings.Contains(s, "statusline"), strings.Contains(s, "status line"):
		return "statusline"
	case strings.Contains(s, "jsonl"), strings.Contains(s, "recording"), strings.Contains(s, "local"):
		return "local"
	case strings.Contains(s, "api"):
		return "api"
	}
	return clip(s, 10)
}

// windowTitle words a window: "5h" is the session, "week (Fable)" the
// Fable weekly window.
func windowTitle(name string) string {
	if model, ok := strings.CutPrefix(name, "week ("); ok {
		return "Weekly · " + strings.TrimSuffix(model, ")")
	}
	switch name {
	case "5h":
		return "Session"
	case "week":
		return "Weekly"
	case "day":
		return "Daily"
	case "month":
		return "Monthly"
	case "":
		return "Window"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// metric is one window on a card.
type metric struct {
	title string
	pct   float64
	reset string
	pace  *plans.WindowPace
}

// detail is a "Label: value" line.
type detail struct {
	label, value string
	tone         rgb
}

// metrics lists a plan's windows, busiest first, or its message window
// when the vendor reports none.
func metrics(r plans.HeadroomReport) []metric {
	out := make([]metric, 0, len(r.Windows)+1)
	for _, w := range r.Windows {
		out = append(out, metric{title: windowTitle(w.Name), pct: w.UsedPct, reset: humanReset(w.ResetsIn), pace: w.Pace})
	}
	if len(out) == 0 && r.WindowCap > 0 {
		out = append(out, metric{title: windowTitle(windowLabel(r.WindowDuration)), pct: r.WindowPct, reset: humanReset(r.WindowResetsIn)})
	}
	if r.SpendLimitUSD > 0 {
		out = append(out, metric{title: "Extra usage", pct: r.SpendPct})
	}
	return out
}

// details lists a plan's spend, credit and cost lines.
func details(r plans.HeadroomReport, cost *spending.ProviderCost, late bool) []detail {
	var out []detail
	switch {
	case r.SpendLimitUSD > 0:
		out = append(out, detail{"This month", money(r.SpendUSD) + " / " + money(r.SpendLimitUSD), porcelain})
	case r.SpendUSD > 0:
		out = append(out, detail{"This month", money(r.SpendUSD) + ", no limit", porcelain})
	}
	if r.BalanceUSD != nil {
		out = append(out, detail{"Credit left", money(*r.BalanceUSD), okC})
	}
	if risk := strings.ToUpper(r.OverageRisk); risk == "MEDIUM" || risk == "HIGH" {
		out = append(out, detail{"Overage risk", risk, map[string]rgb{"MEDIUM": warnC, "HIGH": dangerC}[risk]})
	}
	switch {
	case cost != nil:
		out = append(out, detail{"Today", usage(cost.Today), porcelain}, detail{"30 days", usage(cost.Last30), porcelain})
	case late:
		out = append(out, detail{"Cost", "not read in time", muted})
	case len(r.Windows) == 0 && r.WindowCap == 0 && r.SpendUSD == 0 && r.ConsumedTokens > 0:
		out = append(out, detail{"This month", tokens(r.ConsumedTokens) + " tok", porcelain})
	}
	return out
}

// usage is "$745+ · 2.24B tok": the money at API prices where a plan
// covers it, "+" where some requests have no list price yet, and tokens
// alone where most have none — a figure that looks complete but is not
// is worse than none.
func usage(u spending.Usage) string {
	t := tokens(u.Tokens) + " tok"
	if u.UnpricedShare() > 0.5 {
		return t
	}
	amount := u.CostUSD
	if u.Covered() {
		amount = u.APIEquivalentUSD
	}
	plus := ""
	if u.UnpricedShare() > 0.02 {
		plus = "+"
	}
	return money(amount) + plus + " · " + t
}

// costNotes say what the cost lines stand for and leave out.
func costNotes(cost *spending.ProviderCost) []string {
	if cost == nil {
		return nil
	}
	u := cost.Last30
	var notes []string
	if u.Covered() && u.UnpricedShare() <= 0.5 {
		notes = append(notes, "At API prices; the plan covers it.")
	}
	if share := u.UnpricedShare(); share > 0.02 {
		notes = append(notes, fmt.Sprintf("%.0f%% of requests have no price yet.", share*100))
	}
	return notes
}

// paceLine words a window's pace; its tone warns when it runs out first.
func paceLine(pc *plans.WindowPace) (string, rgb) {
	if pc == nil {
		return "", muted
	}
	switch pc.Status {
	case plans.PaceOnPace:
		return "Pace: on pace", muted
	case plans.PaceBehind:
		return fmt.Sprintf("Pace: behind (%.0f%%) · lasts to reset", pc.DeltaPct), muted
	case plans.PaceUsedUp:
		return "Pace: used up until reset", warnC
	}
	if pc.LastsToReset {
		return fmt.Sprintf("Pace: ahead (+%.0f%%) · lasts to reset", pc.DeltaPct), muted
	}
	return fmt.Sprintf("Pace: ahead (+%.0f%%) · out in %s", pc.DeltaPct, humanDuration(pc.RunsOutIn)), warnC
}

// card draws one plan width columns wide.
func (p painter) card(r plans.HeadroomReport, cost *spending.ProviderCost, late bool, width int) []string {
	inner := width - 4
	side := func(body string) string {
		return p.paint(line, "│ ") + pad(body, inner) + p.paint(line, " │")
	}
	// spread puts left and right at either edge.
	spread := func(left, right string) string {
		return left + strings.Repeat(" ", max(1, inner-visible(left)-visible(right))) + right
	}
	out := []string{p.paint(line, "╭"+strings.Repeat("─", width-2)+"╮")}

	title := p.bold(p.paint(cobaltFg, vendor(r)))
	if b := sourceBadge(r.SignalQuality.Source); b != "" {
		title += " " + p.paint(muted, "["+b+"]")
	}
	plan := ""
	if room := inner - visible(title) - 1; room >= 10 {
		plan = clip("PLAN "+planName(r), room)
		label, value, _ := strings.Cut(plan, " ")
		plan = p.paint(muted, label) + " " + p.bold(p.paint(porcelain, value))
	}
	out = append(out, side(spread(title, plan)), side(p.paint(line, strings.Repeat("─", inner))))

	for i, m := range metrics(r) {
		if i > 0 {
			out = append(out, side(""))
		}
		used := p.paint(levelColor(m.pct), fmt.Sprintf("%.0f%% left", math.Max(0, 100-m.pct)))
		out = append(out, side(spread(p.paint(porcelain, clip(m.title, inner-10)), used)))
		out = append(out, side(p.paint(line, "[ ")+p.bar(m.pct, inner-4)+p.paint(line, " ]")))
		if m.reset != "" {
			out = append(out, side(p.paint(muted, "Resets in "+m.reset)))
		}
		if text, tone := paceLine(m.pace); text != "" {
			out = append(out, side(p.paint(tone, clip(text, inner))))
		}
	}
	ds := details(r, cost, late)
	if len(ds) > 0 && len(out) > 3 {
		out = append(out, side(""))
	}
	for _, d := range ds {
		label := p.paint(muted, d.label+":")
		out = append(out, side(spread(label, p.paint(d.tone, clip(d.value, inner-visible(label)-1)))))
	}
	for _, n := range costNotes(cost) {
		out = append(out, side(p.paint(muted, clip(n, inner))))
	}
	return append(out, p.paint(line, "╰"+strings.Repeat("─", width-2)+"╯"))
}

// layout is how many cards share a row and how wide each is.
func layout(width int) (perRow, cardWidth int) {
	perRow = max(1, (max(width, minCardWidth)+cardGap)/(minCardWidth+cardGap))
	cardWidth = min(maxCardWidth, max(minCardWidth, (width-(perRow-1)*cardGap)/perRow))
	return perRow, cardWidth
}

// sorted orders plans busiest first, as the cards and the table show them.
func sorted(reports []plans.HeadroomReport) []plans.HeadroomReport {
	out := append([]plans.HeadroomReport(nil), reports...)
	sort.SliceStable(out, func(i, j int) bool { return busiest(out[i]) > busiest(out[j]) })
	return out
}

// titleLine is "TokenOps • AI Usage & Limits", the time at the right.
func titleLine(p painter, width int, now time.Time) string {
	left := p.bold(p.paint(cobaltFg, "TokenOps • AI Usage & Limits"))
	if now.IsZero() {
		return left
	}
	stamp := now.Local().Format("Mon 2 Jan 15:04")
	if visible(left)+len(stamp)+1 > width {
		return left
	}
	return left + strings.Repeat(" ", width-visible(left)-len(stamp)) + p.paint(muted, stamp)
}

// Render draws the glance.
func Render(g headroom.Glance, opt Options) string {
	if opt.Width <= 0 {
		opt.Width = 80
	}
	p := painter(opt.Color)
	var b strings.Builder
	b.WriteString(titleLine(p, opt.Width, opt.Now) + "\n\n")
	switch {
	case g.Headroom.Unconfigured != "":
		b.WriteString(p.paint(warnC, g.Headroom.Unconfigured) + "\n")
		return b.String()
	case g.Headroom.StorageDisabled != "":
		b.WriteString(p.paint(warnC, g.Headroom.StorageDisabled) + "\n")
		return b.String()
	}
	if opt.OnlyFindings && opt.Findings != nil {
		return b.String() + strings.TrimPrefix(coachSection(*opt.Findings, opt, p), "\n")
	}
	reports := sorted(g.Headroom.Reports)
	if opt.Brief {
		b.WriteString(brief(reports, opt, p))
	} else {
		b.WriteString(grid(reports, opt, p))
	}
	if opt.Findings != nil {
		b.WriteString(coachSection(*opt.Findings, opt, p))
	}
	for _, n := range g.Headroom.Notes {
		b.WriteString(p.paint(warnC, "! ") + p.paint(muted, n) + "\n")
	}
	return b.String()
}

// grid lays the cards out in rows that fit the terminal.
func grid(reports []plans.HeadroomReport, opt Options, p painter) string {
	perRow, width := layout(opt.Width)
	var b strings.Builder
	for i := 0; i < len(reports); i += perRow {
		group := reports[i:min(i+perRow, len(reports))]
		drawn := make([][]string, len(group))
		height := 0
		for j, r := range group {
			drawn[j] = p.card(r, costFor(opt, r), opt.CostsLate, width)
			height = max(height, len(drawn[j]))
		}
		for at := range height {
			parts := make([]string, 0, len(drawn))
			for _, c := range drawn {
				switch {
				case at < len(c)-1:
					parts = append(parts, pad(c[at], width))
				case at == height-1:
					parts = append(parts, pad(c[len(c)-1], width))
				default:
					// A shorter card is padded to its row's height.
					parts = append(parts, p.paint(line, "│")+strings.Repeat(" ", width-2)+p.paint(line, "│"))
				}
			}
			b.WriteString(strings.TrimRight(strings.Join(parts, strings.Repeat(" ", cardGap)), " ") + "\n")
		}
		if i+perRow < len(reports) {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func costFor(opt Options, r plans.HeadroomReport) *spending.ProviderCost {
	if c, ok := opt.Costs[r.Provider]; ok {
		return &c
	}
	return nil
}

// busiest is a plan's fullest measure, to order the cards.
func busiest(r plans.HeadroomReport) float64 {
	m := r.SpendPct
	for _, w := range r.Windows {
		m = math.Max(m, w.UsedPct)
	}
	return math.Max(m, r.WindowPct)
}

// brief is a table: one line per window, busiest plan first.
func brief(reports []plans.HeadroomReport, opt Options, p painter) string {
	planW := 8
	for _, r := range reports {
		planW = max(planW, utf8.RuneCountInString(vendor(r)+" "+planName(r)))
	}
	planW = min(planW, max(12, opt.Width-50))
	var b strings.Builder
	head := fmt.Sprintf("%-*s  %-16s %5s  %-8s  %s", planW, "PLAN", "WINDOW", "LEFT", "RESETS", "PACE")
	b.WriteString(p.paint(muted, head) + "\n")
	for _, r := range reports {
		name := clip(vendor(r)+" "+planName(r), planW)
		ms := metrics(r)
		if len(ms) == 0 {
			if ds := details(r, costFor(opt, r), false); len(ds) > 0 {
				fmt.Fprintf(&b, "%-*s  %s\n", planW, name, p.paint(muted, ds[0].label+": "+ds[0].value))
			}
			continue
		}
		for i, m := range ms {
			if i > 0 {
				name = ""
			}
			used := p.paint(levelColor(m.pct), fmt.Sprintf("%4.0f%%", math.Max(0, 100-m.pct)))
			pace, tone := briefPace(m.pace)
			fmt.Fprintf(&b, "%-*s  %-16s %s  %-8s  %s\n", planW, name, clip(m.title, 16), used, m.reset, p.paint(tone, pace))
		}
	}
	return b.String()
}

// briefPace is a pace in a table cell.
func briefPace(pc *plans.WindowPace) (string, rgb) {
	switch {
	case pc == nil:
		return "", muted
	case pc.Status == plans.PaceOnPace:
		return "on pace", muted
	case pc.Status == plans.PaceUsedUp:
		return "used up", warnC
	case pc.LastsToReset:
		return fmt.Sprintf("%+.0f%% · lasts", pc.DeltaPct), muted
	}
	return fmt.Sprintf("%+.0f%% · out in %s", pc.DeltaPct, humanDuration(pc.RunsOutIn)), warnC
}

// windowLabel names a message window from its Go duration.
func windowLabel(s string) string {
	d, _ := time.ParseDuration(s)
	switch {
	case d >= 7*24*time.Hour:
		return "week"
	case d > 0:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return ""
}

// tokens is "2.24B", "198M", "15k".
func tokens(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.2fB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.0fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// levelMark is a finding's mark and colour.
func levelMark(level string) (string, rgb) {
	switch level {
	case findings.LevelWarn:
		return "▲", warnC
	case findings.LevelNotice:
		return "●", cobaltFg
	}
	return "·", muted
}

// coachSection lists the findings under the cards: a mark and a title,
// the evidence beneath it, and what to do. Brief keeps the titles only.
func coachSection(r findings.Report, opt Options, p painter) string {
	var b strings.Builder
	head := p.bold(p.paint(cobaltFg, "Coach"))
	if len(r.Findings) == 0 {
		head += p.paint(muted, " · nothing stands out")
	} else {
		head += p.paint(muted, fmt.Sprintf(" · %d finding%s", len(r.Findings), plural(len(r.Findings))))
	}
	if r.SessionsReadAt != nil && !opt.Now.IsZero() {
		head += p.paint(muted, " · sessions read "+ago(opt.Now.Sub(*r.SessionsReadAt)))
	}
	b.WriteString("\n" + head + "\n")
	width := min(opt.Width, 100) - 4
	shown := r.Findings
	if len(shown) > maxFindings && !opt.OnlyFindings {
		shown = shown[:maxFindings]
	}
	for _, f := range shown {
		mark, tone := levelMark(f.Level)
		b.WriteString("  " + p.paint(tone, mark) + " " + p.bold(p.paint(porcelain, clip(f.Title, width))) + "\n")
		if opt.Brief {
			continue
		}
		for _, l := range wrap(f.Evidence, width) {
			b.WriteString("    " + p.paint(muted, l) + "\n")
		}
		inCode := false
		for i, l := range wrap(f.Action, width-2) {
			lead := "  "
			if i == 0 {
				lead = p.paint(cobaltFg, "→ ")
			}
			var painted string
			painted, inCode = p.code(l, inCode)
			b.WriteString("    " + lead + painted + "\n")
		}
	}
	if more := len(r.Findings) - len(shown); more > 0 {
		b.WriteString(p.paint(muted, fmt.Sprintf("  + %d more: tokenops glance --findings", more)) + "\n")
	}
	return b.String()
}

// code paints line with its `quoted` commands in the accent colour and
// without their backticks; inCode carries a command across a line break.
func (p painter) code(line string, inCode bool) (string, bool) {
	var b strings.Builder
	for i, part := range strings.Split(line, "`") {
		if i > 0 {
			inCode = !inCode
		}
		if part == "" {
			continue
		}
		if inCode {
			b.WriteString(p.bold(p.paint(cobaltFg, part)))
		} else {
			b.WriteString(p.paint(porcelain, part))
		}
	}
	return b.String(), inCode
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ago is "just now", "12m ago", "3h ago".
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}

// wrap breaks plain s into lines of at most width runes at spaces.
func wrap(s string, width int) []string {
	if s == "" {
		return nil
	}
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		switch {
		case line == "":
			line = w
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	return append(lines, line)
}
