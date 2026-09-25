package mcp

import (
	"reflect"
	"testing"
)

// Each excluded activity source can be opted in by name.
func TestIncludeSourcesReadmitsOnlyNamed(t *testing.T) {
	cases := []struct {
		name    string
		sources []string
		want    []string
	}{
		{"nothing named leaves the defaults alone", nil, nil},
		{"activity proxy alone", []string{"mcp-session"}, []string{"mcp-session"}},
		{"duplicates collapse", []string{"mcp-session", "mcp-session"}, []string{"mcp-session"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveIncludeSources(tc.sources)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveIncludeSources(%v) = %v; want %v", tc.sources, got, tc.want)
			}
		})
	}
}

// Every cost-surface tool carries the same opt-in, so an operator does
// not have to learn a different spelling per tool.
func TestSpendSummaryInputCarriesIncludeSources(t *testing.T) {
	f, err := spendSummaryInput{IncludeSources: []string{"mcp-session"}}.toFilter()
	if err != nil {
		t.Fatalf("toFilter: %v", err)
	}
	if !reflect.DeepEqual(f.IncludeSources, []string{"mcp-session"}) {
		t.Errorf("IncludeSources = %v; want [mcp-session]", f.IncludeSources)
	}
	// Default exclusions still apply unless a named source is re-admitted.
	if f.ExcludeSources != nil {
		t.Errorf("ExcludeSources = %v; want nil (defaults still apply)", f.ExcludeSources)
	}
}
