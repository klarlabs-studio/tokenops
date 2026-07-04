package svgchart

import (
	"fmt"
	"strings"
)

// Series is one named line/band of a time-series chart. Values align 1:1 with
// the chart's x labels. Highlight paints it in full accent (the point of the
// chart) vs. a recessive tint.
type Series struct {
	Name      string
	Values    []float64
	Highlight bool
}

// Lines renders a multi-series line chart over a shared, single linear y-axis
// (never dual-axis — a series that is tiny next to another correctly reads as
// a hairline near the baseline, which is often the whole point). Identity is
// carried by a direct label at each line's right end, so it stays
// colorblind-safe.
func Lines(title string, xLabels []string, series []Series, opts Options) string {
	opts = opts.withDefaults()
	const (
		padX      = 48
		padTop    = 52
		plotH     = 200
		padBottom = 52
		labelPadX = 6
	)
	w := opts.Width
	plotW := w - padX - 90 // room for right-hand end labels
	h := padTop + plotH + padBottom

	maxV := 0.0
	for _, s := range series {
		for _, v := range s.Values {
			if v > maxV {
				maxV = v
			}
		}
	}
	if maxV <= 0 {
		maxV = 1
	}
	n := len(xLabels)
	xAt := func(i int) float64 {
		if n <= 1 {
			return float64(padX)
		}
		return float64(padX) + float64(i)/float64(n-1)*float64(plotW)
	}
	yAt := func(v float64) float64 {
		return float64(padTop) + (1-v/maxV)*float64(plotH)
	}

	var b strings.Builder
	svgOpen(&b, w, h, title)
	svgTitle(&b, padX, title)

	// Baseline + top gridline (recessive).
	fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%.1f" y2="%.1f" stroke="currentColor" opacity="0.15"/>`,
		padX, yAt(0), xAt(n-1), yAt(0))
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%.1f" y2="%d" stroke="currentColor" opacity="0.08"/>`,
		padX, padTop, xAt(n-1), padTop)

	for _, s := range series {
		if len(s.Values) == 0 {
			continue
		}
		pts := make([]string, 0, len(s.Values))
		for i, v := range s.Values {
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", xAt(i), yAt(v)))
		}
		stroke := "currentColor"
		strokeOpacity, strokeW := "0.4", "1.5"
		if s.Highlight {
			stroke, strokeOpacity, strokeW = opts.Accent, "1", "2.5"
		}
		fmt.Fprintf(&b,
			`<polyline points="%s" fill="none" stroke="%s" stroke-opacity="%s" stroke-width="%s" stroke-linejoin="round" stroke-linecap="round"/>`,
			strings.Join(pts, " "), stroke, strokeOpacity, strokeW)
		// Direct end label carries identity.
		last := len(s.Values) - 1
		lblColor, lblOpacity := "currentColor", "0.75"
		if s.Highlight {
			lblColor, lblOpacity = opts.Accent, "1"
		}
		fmt.Fprintf(&b,
			`<text x="%.1f" y="%.1f" fill="%s" opacity="%s" font-size="12" font-weight="600" dominant-baseline="middle">%s</text>`,
			xAt(last)+labelPadX, yAt(s.Values[last]), lblColor, lblOpacity, escapeText(s.Name))
	}

	// X labels (first, middle, last to avoid crowding).
	for _, i := range axisTicks(n) {
		fmt.Fprintf(&b,
			`<text x="%.1f" y="%d" fill="currentColor" opacity="0.5" font-size="11" text-anchor="middle">%s</text>`,
			xAt(i), padTop+plotH+22, escapeText(xLabels[i]))
	}

	svgCaption(&b, padX, h, opts.Caption)
	b.WriteString(`</svg>`)
	return b.String()
}

// StackedArea renders a 100%-stacked area chart: at each x the series are
// stacked as shares of that column's total, so the chart shows how a
// composition drifts over time. A legend names the bands (identity is never
// color-alone); bands use the accent at descending opacity in stack order.
func StackedArea(title string, xLabels []string, series []Series, opts Options) string {
	opts = opts.withDefaults()
	const (
		padX      = 24
		padTop    = 64
		plotH     = 200
		padBottom = 52
	)
	w := opts.Width
	plotW := w - 2*padX
	h := padTop + plotH + padBottom
	n := len(xLabels)

	xAt := func(i int) float64 {
		if n <= 1 {
			return float64(padX)
		}
		return float64(padX) + float64(i)/float64(n-1)*float64(plotW)
	}
	// Column totals for share math.
	totals := make([]float64, n)
	for _, s := range series {
		for i := 0; i < n && i < len(s.Values); i++ {
			totals[i] += s.Values[i]
		}
	}
	// frac[i] = share of column i for the current series; cumulative runs
	// bottom (0) to top (1) as we stack.
	yAt := func(frac float64) float64 {
		return float64(padTop) + (1-frac)*float64(plotH)
	}

	var b strings.Builder
	svgOpen(&b, w, h, title)
	svgTitle(&b, padX, title)

	opacities := []string{"0.72", "0.5", "0.34", "0.18", "0.1"}
	cum := make([]float64, n) // cumulative share per column, bottom-up
	for j, s := range series {
		// Top edge (cum+share) left→right, then bottom edge (cum) right→left.
		top := make([]string, 0, n)
		bottom := make([]string, 0, n)
		next := make([]float64, n)
		for i := 0; i < n; i++ {
			share := 0.0
			if totals[i] > 0 && i < len(s.Values) {
				share = s.Values[i] / totals[i]
			}
			next[i] = cum[i] + share
			top = append(top, fmt.Sprintf("%.1f,%.1f", xAt(i), yAt(next[i])))
		}
		for i := n - 1; i >= 0; i-- {
			bottom = append(bottom, fmt.Sprintf("%.1f,%.1f", xAt(i), yAt(cum[i])))
		}
		op := opacities[j%len(opacities)]
		fmt.Fprintf(&b,
			`<polygon points="%s %s" fill="%s" fill-opacity="%s" stroke="currentColor" stroke-opacity="0.06"/>`,
			strings.Join(top, " "), strings.Join(bottom, " "), opts.Accent, op)
		copy(cum, next)
	}

	// Legend row (identity, never color-alone).
	lx := padX
	for j, s := range series {
		op := opacities[j%len(opacities)]
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="11" height="11" rx="2" fill="%s" fill-opacity="%s"/>`,
			lx, padTop-18, opts.Accent, op)
		fmt.Fprintf(&b, `<text x="%d" y="%d" fill="currentColor" font-size="12" font-weight="500">%s</text>`,
			lx+16, padTop-8, escapeText(s.Name))
		lx += 20 + 8*len(s.Name) + 24
	}

	for _, i := range axisTicks(n) {
		fmt.Fprintf(&b,
			`<text x="%.1f" y="%d" fill="currentColor" opacity="0.5" font-size="11" text-anchor="middle">%s</text>`,
			xAt(i), padTop+plotH+22, escapeText(xLabels[i]))
	}

	svgCaption(&b, padX, h, opts.Caption)
	b.WriteString(`</svg>`)
	return b.String()
}

// axisTicks picks a few evenly-spaced x indices (first, middle, last) so the
// axis never crowds regardless of series length.
func axisTicks(n int) []int {
	switch {
	case n <= 0:
		return nil
	case n == 1:
		return []int{0}
	case n <= 6:
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	default:
		return []int{0, n / 4, n / 2, 3 * n / 4, n - 1}
	}
}

func svgOpen(b *strings.Builder, w, h int, title string) {
	fmt.Fprintf(b,
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="%s" font-family="system-ui, -apple-system, Segoe UI, sans-serif">`,
		w, h, w, h, escapeAttr(title))
	fmt.Fprintf(b, `<title>%s</title>`, escapeText(title))
}

func svgTitle(b *strings.Builder, padX int, title string) {
	fmt.Fprintf(b,
		`<text x="%d" y="30" fill="currentColor" font-size="17" font-weight="600">%s</text>`,
		padX, escapeText(title))
}

func svgCaption(b *strings.Builder, padX, h int, caption string) {
	if caption == "" {
		return
	}
	fmt.Fprintf(b,
		`<text x="%d" y="%d" fill="currentColor" opacity="0.45" font-size="11">%s</text>`,
		padX, h-12, escapeText(caption))
}
