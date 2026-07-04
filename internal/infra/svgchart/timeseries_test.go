package svgchart

import (
	"strings"
	"testing"
)

func TestLines_RendersSeriesAndLabels(t *testing.T) {
	svg := Lines("Tok", []string{"Jun 01", "Jun 08", "Jun 15"}, []Series{
		{Name: "input", Values: []float64{100, 200, 150}},
		{Name: "output", Values: []float64{1, 2, 1}, Highlight: true},
	}, Options{Accent: "#123456", Caption: "cap"})
	for _, want := range []string{"<svg", "polyline", "input", "output", "#123456", "Jun 01", "</svg>"} {
		if !strings.Contains(svg, want) {
			t.Errorf("Lines svg missing %q", want)
		}
	}
	// Highlighted series uses the accent stroke; recessive uses currentColor.
	if !strings.Contains(svg, `stroke="#123456"`) {
		t.Error("highlighted series should stroke in accent")
	}
	if !strings.Contains(svg, `stroke="currentColor"`) {
		t.Error("recessive series should stroke in currentColor")
	}
}

func TestStackedArea_RendersBandsAndLegend(t *testing.T) {
	svg := StackedArea("Comp", []string{"w1", "w2"}, []Series{
		{Name: "Read", Values: []float64{60, 50}},
		{Name: "Bash", Values: []float64{30, 30}},
		{Name: "Prose", Values: []float64{10, 20}},
	}, Options{})
	for _, want := range []string{"polygon", "Read", "Bash", "Prose", "<rect"} {
		if !strings.Contains(svg, want) {
			t.Errorf("StackedArea svg missing %q", want)
		}
	}
}

func TestLines_EmptyValuesDoesNotPanic(t *testing.T) {
	_ = Lines("x", nil, []Series{{Name: "a", Values: nil}}, Options{})
	_ = StackedArea("x", nil, nil, Options{})
}
