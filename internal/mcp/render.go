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

// renderBurnSummary builds a markdown summary for the burn-rate
// response: total cost + period + inline sparkline of the hourly
// series. The series carries the per-bucket cost in USD; callers
// flatten the [{BucketStart,CostUSD,...}] rows into a []float64 to
// drive the spark.
func renderBurnSummary(hours int, totalCost float64, currency string, series []float64) string {
	var s strings.Builder
	fmt.Fprintf(&s, "## Burn rate — last %dh\n\n", hours)
	if spark := Sparkline(series, SparklineOptions{Label: fmt.Sprintf("burn last %dh", hours)}); spark != "" {
		s.WriteString(spark)
		s.WriteString("\n\n")
	}
	fmt.Fprintf(&s, "| Total | %.4f %s |\n", totalCost, currency)
	fmt.Fprintf(&s, "| Buckets | %d hourly |\n", len(series))
	if len(series) > 0 {
		min, max := series[0], series[0]
		for _, v := range series {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
		fmt.Fprintf(&s, "| Range | %.4f .. %.4f %s |\n", min, max, currency)
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
