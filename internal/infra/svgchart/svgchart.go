// Package svgchart renders small, dependency-free, self-contained SVG charts
// for tokenops analyses. Text uses currentColor so an inlined chart themes
// with the surrounding page (light/dark); bars use a fixed accent. The
// output is deterministic given its inputs — the same report always yields
// the same SVG, so a chart embedded in a doc is reproducible from the CLI.
package svgchart

import (
	"fmt"
	"strings"
)

// Bar is one horizontal bar. Frac is 0..1 of the track width; Display is the
// text shown at the bar end (e.g. "46.5%"); Highlight paints it in full
// accent (the point of the chart) vs. a recessive tint.
type Bar struct {
	Label     string
	Display   string
	Frac      float64
	Note      string
	Highlight bool
}

// Options tunes an HBars render.
type Options struct {
	Width   int    // default 640
	Accent  string // default #6366F1
	Caption string // small attribution line, e.g. "tokenops fmt analyze"
}

func (o Options) withDefaults() Options {
	if o.Width <= 0 {
		o.Width = 640
	}
	if o.Accent == "" {
		o.Accent = "#6366F1"
	}
	return o
}

// HBars renders a titled horizontal-bar chart. Identity is carried by direct
// labels, not color, so it is colorblind-safe by construction; only emphasis
// is encoded in hue (highlighted bar = full accent).
func HBars(title string, bars []Bar, opts Options) string {
	opts = opts.withDefaults()
	const (
		padX    = 24
		padTop  = 52
		rowH    = 52
		barH    = 12
		labelDY = 16
		trackDY = 30
		noteDY  = 46
		footerH = 34
	)
	w := opts.Width
	trackW := w - 2*padX
	h := padTop + rowH*len(bars) + footerH

	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="%s" font-family="system-ui, -apple-system, Segoe UI, sans-serif">`,
		w, h, w, h, escapeAttr(title))
	fmt.Fprintf(&b, `<title>%s</title>`, escapeText(title))

	// Title.
	fmt.Fprintf(&b,
		`<text x="%d" y="30" fill="currentColor" font-size="17" font-weight="600">%s</text>`,
		padX, escapeText(title))

	for i, bar := range bars {
		y := padTop + i*rowH
		fillW := bar.Frac * float64(trackW)
		if fillW < 3 {
			fillW = 3
		}
		fill := opts.Accent
		fillOpacity := "1"
		if !bar.Highlight {
			fillOpacity = "0.34"
		}
		// Label (left) + value (right).
		fmt.Fprintf(&b,
			`<text x="%d" y="%d" fill="currentColor" font-size="13" font-weight="500">%s</text>`,
			padX, y+labelDY, escapeText(bar.Label))
		fmt.Fprintf(&b,
			`<text x="%d" y="%d" fill="currentColor" opacity="0.7" font-size="13" font-weight="600" text-anchor="end">%s</text>`,
			w-padX, y+labelDY, escapeText(bar.Display))
		// Track + fill.
		fmt.Fprintf(&b,
			`<rect x="%d" y="%d" width="%d" height="%d" rx="6" fill="currentColor" opacity="0.10"/>`,
			padX, y+trackDY-barH, trackW, barH)
		fmt.Fprintf(&b,
			`<rect x="%d" y="%d" width="%.1f" height="%d" rx="6" fill="%s" fill-opacity="%s"/>`,
			padX, y+trackDY-barH, fillW, barH, opts.Accent, fillOpacity)
		if bar.Note != "" {
			fmt.Fprintf(&b,
				`<text x="%d" y="%d" fill="currentColor" opacity="0.55" font-size="11">%s</text>`,
				padX, y+noteDY, escapeText(bar.Note))
		}
		_ = fill
	}

	if opts.Caption != "" {
		fmt.Fprintf(&b,
			`<text x="%d" y="%d" fill="currentColor" opacity="0.45" font-size="11">%s</text>`,
			padX, h-12, escapeText(opts.Caption))
	}
	b.WriteString(`</svg>`)
	return b.String()
}

func escapeAttr(s string) string {
	r := strings.NewReplacer(`&`, "&amp;", `"`, "&quot;", `<`, "&lt;", `>`, "&gt;")
	return r.Replace(s)
}

func escapeText(s string) string {
	r := strings.NewReplacer(`&`, "&amp;", `<`, "&lt;", `>`, "&gt;")
	return r.Replace(s)
}
