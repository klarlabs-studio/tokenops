package cli

import (
	"sort"
	"testing"

	"go.klarlabs.de/tokenops/internal/infra/jsonlfmt"
)

func selectedIDs(t *testing.T, selection string) []string {
	t.Helper()
	defs := analyzeChartDefs(&jsonlfmt.Report{})
	want, err := resolveChartSelection(selection, defs)
	if err != nil {
		t.Fatalf("resolve %q: %v", selection, err)
	}
	out := make([]string, 0, len(want))
	for id := range want {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestResolveChartSelection(t *testing.T) {
	all := []string{"composition", "composition-over-time", "fmt-roi", "reads", "tokens-over-time", "volume-over-time"}
	bars := []string{"composition", "fmt-roi", "reads"}
	timeline := []string{"composition-over-time", "tokens-over-time", "volume-over-time"}

	cases := map[string][]string{
		"":                              all, // default → all
		"all":                           all,
		"bars":                          bars,
		"timeline":                      timeline,
		"composition-over-time":         {"composition-over-time"},
		"composition, tokens-over-time": {"composition", "tokens-over-time"},
		"bars,composition-over-time":    {"composition", "composition-over-time", "fmt-roi", "reads"},
		"timeline,timeline":             timeline, // dedup
	}
	for sel, want := range cases {
		if got := selectedIDs(t, sel); !eq(got, want) {
			t.Errorf("resolve(%q) = %v, want %v", sel, got, want)
		}
	}
}

func TestResolveChartSelectionUnknown(t *testing.T) {
	defs := analyzeChartDefs(&jsonlfmt.Report{})
	if _, err := resolveChartSelection("nope", defs); err == nil {
		t.Fatal("expected an error for an unknown chart id")
	}
}
