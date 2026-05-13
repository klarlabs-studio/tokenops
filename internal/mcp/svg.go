package mcp

import (
	"fmt"
	"strings"
)

// SVG generators kept tiny and dependency-free: every chart is hand-
// rolled SVG so we don't pull a charting library into the daemon and
// the output stays render-safe inline in markdown (Desktop, Code,
// Cursor all accept <svg>). Three primitives — sparkline, bar, gauge
// — cover every chart the cost/headroom tools currently surface.

// SparklineOptions tunes a Sparkline render. Width / Height default
// to 240×40; Color falls back to a neutral grey.
type SparklineOptions struct {
	Width  int
	Height int
	Color  string
	Label  string
}

// Sparkline renders a line chart over the given series. Empty input
// returns an empty string so callers can safely concatenate.
func Sparkline(series []float64, opts SparklineOptions) string {
	if len(series) == 0 {
		return ""
	}
	if opts.Width <= 0 {
		opts.Width = 240
	}
	if opts.Height <= 0 {
		opts.Height = 40
	}
	if opts.Color == "" {
		opts.Color = "#4159d6"
	}
	min, max := series[0], series[0]
	for _, v := range series {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	span := max - min
	if span == 0 {
		span = 1
	}
	step := float64(opts.Width) / float64(len(series)-1+1)
	var pts strings.Builder
	for i, v := range series {
		x := float64(i) * step
		y := float64(opts.Height) - ((v - min) / span * float64(opts.Height-2)) - 1
		if i > 0 {
			pts.WriteByte(' ')
		}
		fmt.Fprintf(&pts, "%.1f,%.1f", x, y)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" aria-label="%s">`,
		opts.Width, opts.Height, opts.Width, opts.Height, escapeAttr(opts.Label))
	fmt.Fprintf(&b, `<polyline fill="none" stroke="%s" stroke-width="1.5" points="%s"/>`, opts.Color, pts.String())
	b.WriteString(`</svg>`)
	return b.String()
}

// HeadroomGauge renders a horizontal bar showing window_consumed vs.
// window_cap, coloured by overage risk band. Used in session_budget
// to give operators a one-glance sense of how close they are to the
// cap.
func HeadroomGauge(consumed, cap int64, opts SparklineOptions) string {
	if cap <= 0 {
		return ""
	}
	if opts.Width <= 0 {
		opts.Width = 280
	}
	if opts.Height <= 0 {
		opts.Height = 18
	}
	pct := float64(consumed) / float64(cap)
	if pct > 1 {
		pct = 1
	}
	if pct < 0 {
		pct = 0
	}
	fill := "#3aa55d" // green
	switch {
	case pct >= 0.8:
		fill = "#d04040" // red
	case pct >= 0.6:
		fill = "#d09a40" // amber
	}
	filledWidth := pct * float64(opts.Width)
	label := opts.Label
	if label == "" {
		label = fmt.Sprintf("%d of %d", consumed, cap)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" aria-label="%s">`,
		opts.Width, opts.Height, opts.Width, opts.Height, escapeAttr(label))
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%d" height="%d" fill="#e6e6e6" rx="3"/>`, opts.Width, opts.Height)
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%.1f" height="%d" fill="%s" rx="3"/>`, filledWidth, opts.Height, fill)
	fmt.Fprintf(&b, `<text x="6" y="%d" font-family="ui-monospace,monospace" font-size="11" fill="#222">%s (%.1f%%)</text>`,
		opts.Height-5, escapeText(label), pct*100)
	b.WriteString(`</svg>`)
	return b.String()
}

func escapeAttr(s string) string {
	r := strings.NewReplacer(`"`, `&quot;`, `<`, `&lt;`, `>`, `&gt;`, `&`, `&amp;`)
	return r.Replace(s)
}

func escapeText(s string) string {
	r := strings.NewReplacer(`<`, `&lt;`, `>`, `&gt;`, `&`, `&amp;`)
	return r.Replace(s)
}
