package svgchart

import (
	"strings"
	"testing"
)

func TestHBars_WellFormedAndLabeled(t *testing.T) {
	svg := HBars("Where tokens go", []Bar{
		{Label: "File reads", Display: "46.5%", Frac: 0.465, Highlight: true, Note: "source files"},
		{Label: "Command output", Display: "26.6%", Frac: 0.266},
	}, Options{Caption: "tokenops fmt analyze"})

	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("not a well-formed svg element: %.40s...", svg)
	}
	for _, want := range []string{"Where tokens go", "File reads", "46.5%", "Command output", "26.6%", "tokenops fmt analyze", "currentColor"} {
		if !strings.Contains(svg, want) {
			t.Errorf("svg missing %q", want)
		}
	}
	// Highlighted bar full accent; recessive bar has fill-opacity.
	if !strings.Contains(svg, `fill-opacity="1"`) || !strings.Contains(svg, `fill-opacity="0.34"`) {
		t.Errorf("expected both full and recessive fills")
	}
	// Deterministic: same input -> same output.
	if HBars("Where tokens go", []Bar{{Label: "File reads", Display: "46.5%", Frac: 0.465, Highlight: true, Note: "source files"}, {Label: "Command output", Display: "26.6%", Frac: 0.266}}, Options{Caption: "tokenops fmt analyze"}) != svg {
		t.Error("HBars is not deterministic")
	}
}

func TestHBars_EscapesText(t *testing.T) {
	svg := HBars("A & B <x>", []Bar{{Label: "a<b", Display: "1", Frac: 0.5}}, Options{})
	if strings.Contains(svg, "<x>") || strings.Contains(svg, "a<b") {
		t.Error("unescaped markup leaked into SVG text")
	}
	if !strings.Contains(svg, "A &amp; B &lt;x&gt;") {
		t.Error("title not escaped as expected")
	}
}
