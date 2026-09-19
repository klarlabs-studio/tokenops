package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// markdownPayload wraps a human-friendly markdown summary and a
// structured payload (typically a Go struct or map) into a single
// MCP text response. The result renders as styled markdown in
// Claude Desktop / Code / Cursor while keeping the JSON appendix
// agents can parse mechanically.
//
// Layout:
//
//	<summary markdown>
//
//	<details><summary>JSON</summary>
//
//	```json
//	{ ... structured ... }
//	```
//
//	</details>
//
// Clients that don't render <details> fall through to showing both
// blocks linearly — still useful, just less compact.
func markdownPayload(summary string, structured any) string {
	out, err := json.MarshalIndent(structured, "", "  ")
	if err != nil {
		// Fallback: surface the marshal error in the response so
		// operators see it instead of a silent dropped payload.
		out = fmt.Appendf(nil, "// marshal error: %v", err)
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(summary, "\n"))
	b.WriteString("\n\n<details><summary>JSON</summary>\n\n```json\n")
	b.Write(out)
	b.WriteString("\n```\n\n</details>\n")
	return b.String()
}

// renderBudgetSummary builds the human-friendly markdown table for a
// SessionBudget response. Falls back to a one-line note when the plan
// has no concrete window cap. Inlines an SVG headroom gauge above
// the table so the operator's eye lands on the bar before reading
// the numbers — fastest possible visceral read.
func renderBudgetSummary(b budgetSummaryRow) string {
	var s strings.Builder
	fmt.Fprintf(&s, "## %s — `%s`\n\n", b.Display, b.RecommendedAction)
	if b.WindowCap > 0 {
		gauge := HeadroomGauge(b.WindowConsumed, b.WindowCap, SparklineOptions{
			Label: fmt.Sprintf("%d / %d %s", b.WindowConsumed, b.WindowCap, b.WindowUnit),
		})
		if gauge != "" {
			s.WriteString(gauge)
			s.WriteString("\n\n")
		}
		fmt.Fprintf(&s, "| Metric | Value |\n|---|---|\n")
		fmt.Fprintf(&s, "| Window | %d / %d %s (%.1f%%) |\n", b.WindowConsumed, b.WindowCap, b.WindowUnit, b.WindowPct)
		if b.WillHitCapWithin != "" {
			fmt.Fprintf(&s, "| ETA to cap | %s |\n", b.WillHitCapWithin)
		}
		fmt.Fprintf(&s, "| Resets in | %s |\n", b.WindowResetsIn)
		fmt.Fprintf(&s, "| Burn rate | %.1f / hour |\n", b.RecentRatePerHour)
		fmt.Fprintf(&s, "| Confidence | %s |\n", b.Confidence)
		fmt.Fprintf(&s, "| Signal | `%s` — %s |\n", b.SignalLevel, b.SignalCaveat)
	} else if b.Note != "" {
		fmt.Fprintf(&s, "_%s_\n", b.Note)
	}
	return s.String()
}

// burnTotals is the burn-rate window flattened for rendering: totals plus
// the hourly series in both units, so the renderer can plot whichever one
// carries the variation.
type burnTotals struct {
	Hours         int
	Currency      string
	Cost          float64
	Tokens        int64
	APIEquivalent float64
	CostSeries    []float64
	TokenSeries   []float64
}

// renderBurnSummary builds a markdown summary for the burn-rate
// response: the window's burn, an inline sparkline of the hourly series,
// and the hourly range.
//
// It leads with tokens when the window cost nothing. A Claude Max
// operator was shown "Total 0.0000 USD" for 2.2 billion tokens — true of
// the bill and useless as a burn rate. `tokenops spend` phrases the same
// case in tokens; so does this.
func renderBurnSummary(b burnTotals) string {
	planCovered := b.Cost == 0 && b.Tokens > 0
	series, unit, format := b.CostSeries, b.Currency, "%.4f"
	if planCovered {
		series, unit, format = b.TokenSeries, "tokens", "%.0f"
	}

	var s strings.Builder
	fmt.Fprintf(&s, "## Burn rate — last %dh\n\n", b.Hours)
	if spark := Sparkline(series, SparklineOptions{Label: fmt.Sprintf("burn last %dh (%s)", b.Hours, unit)}); spark != "" {
		s.WriteString(spark)
		s.WriteString("\n\n")
	}
	s.WriteString("| Metric | Value |\n|---|---|\n")
	if planCovered {
		fmt.Fprintf(&s, "| Tokens | %d tokens (plan-covered, so $0 at the margin) |\n", b.Tokens)
		fmt.Fprintf(&s, "| API equivalent | %.2f %s |\n", b.APIEquivalent, b.Currency)
	} else {
		fmt.Fprintf(&s, "| Total | %.4f %s |\n", b.Cost, b.Currency)
		fmt.Fprintf(&s, "| Tokens | %d |\n", b.Tokens)
		if b.APIEquivalent > b.Cost {
			fmt.Fprintf(&s, "| API equivalent | %.2f %s |\n", b.APIEquivalent, b.Currency)
		}
	}
	fmt.Fprintf(&s, "| Buckets | %d hourly |\n", len(series))
	if len(series) > 0 {
		lo, hi := series[0], series[0]
		for _, v := range series {
			lo, hi = min(lo, v), max(hi, v)
		}
		fmt.Fprintf(&s, "| Range | "+format+" .. "+format+" %s |\n", lo, hi, unit)
	}
	return s.String()
}

// budgetSummaryRow is the minimal flat view the renderer needs. The
// caller flattens plans.SessionBudget into this so the renderer
// doesn't depend on the plans package (keeps the mcp package's
// rendering helpers reusable).
type budgetSummaryRow struct {
	Display           string
	WindowConsumed    int64
	WindowCap         int64
	WindowUnit        string
	WindowPct         float64
	WindowResetsIn    string
	WillHitCapWithin  string
	RecentRatePerHour float64
	Confidence        string
	RecommendedAction string
	SignalLevel       string
	SignalCaveat      string
	Note              string
}
