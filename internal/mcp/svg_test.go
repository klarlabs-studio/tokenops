package mcp

import (
	"strings"
	"testing"
)

// Sparkline renders an SVG containing a polyline whose point count
// matches the series. Empty input must return an empty string so
// callers can concatenate without producing dangling elements.
func TestSparkline(t *testing.T) {
	if got := Sparkline(nil, SparklineOptions{}); got != "" {
		t.Fatalf("empty series should produce empty SVG; got %q", got)
	}
	out := Sparkline([]float64{1, 2, 3, 4, 5}, SparklineOptions{Label: "burn"})
	if !strings.Contains(out, "<svg") || !strings.Contains(out, "<polyline") {
		t.Fatalf("sparkline missing svg/polyline: %q", out)
	}
	if !strings.Contains(out, `aria-label="burn"`) {
		t.Fatalf("sparkline missing aria-label: %q", out)
	}
	// Five points => five comma-separated coords on the polyline.
	if got := strings.Count(out, ","); got != 5 {
		t.Fatalf("expected 5 coord pairs (one per point), got %d in %q", got, out)
	}
}

// HeadroomGauge colours by percent: <60% green, 60-80% amber, 80%+ red.
// The label must contain the percent so the gauge is self-describing.
func TestHeadroomGauge(t *testing.T) {
	cases := []struct {
		name             string
		consumed, cap    int64
		wantFillContains string
	}{
		{"green", 10, 100, "#3aa55d"},
		{"amber", 70, 100, "#d09a40"},
		{"red", 90, 100, "#d04040"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := HeadroomGauge(c.consumed, c.cap, SparklineOptions{})
			if !strings.Contains(out, c.wantFillContains) {
				t.Fatalf("%s: expected fill %s in %q", c.name, c.wantFillContains, out)
			}
		})
	}
	if got := HeadroomGauge(0, 0, SparklineOptions{}); got != "" {
		t.Fatalf("zero cap must produce empty SVG; got %q", got)
	}
}

// escapeAttr must escape every character that would break out of a
// double-quoted attribute, otherwise a maliciously named tool label
// could inject SVG content.
func TestEscapeAttr(t *testing.T) {
	got := escapeAttr(`bad "label" & <stuff>`)
	for _, frag := range []string{`&quot;`, `&amp;`, `&lt;`, `&gt;`} {
		if !strings.Contains(got, frag) {
			t.Fatalf("escapeAttr missing %s: %q", frag, got)
		}
	}
}
