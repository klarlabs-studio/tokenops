package mcp

import (
	"reflect"
	"testing"
)

// `include_demo: true` used to clear the whole exclude list, so an
// operator asking for synthetic seeds also got the MCP activity-proxy
// pings folded in. Each source is now opted in by name.
func TestIncludeSourcesReadmitsOnlyNamed(t *testing.T) {
	cases := []struct {
		name        string
		sources     []string
		includeDemo bool
		want        []string
	}{
		{"nothing named leaves the defaults alone", nil, false, nil},
		{"alias expands to demo", nil, true, []string{"demo"}},
		{"named sources pass through", []string{"demo", "mcp-session"}, false, []string{"demo", "mcp-session"}},
		{"activity proxy alone", []string{"mcp-session"}, false, []string{"mcp-session"}},
		{"alias composes without duplicating", []string{"demo"}, true, []string{"demo"}},
		{"alias appends to a disjoint list", []string{"mcp-session"}, true, []string{"mcp-session", "demo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveIncludeSources(tc.sources, tc.includeDemo)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveIncludeSources(%v, %v) = %v; want %v", tc.sources, tc.includeDemo, got, tc.want)
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
	// The old blanket opt-out must be gone: clearing ExcludeSources
	// re-admitted every source at once.
	if f.ExcludeSources != nil {
		t.Errorf("ExcludeSources = %v; want nil (defaults still apply)", f.ExcludeSources)
	}
}

// The back-compat alias keeps working for anyone with `include_demo`
// in a saved prompt or a pasted doc.
func TestSpendSummaryInputHonoursIncludeDemoAlias(t *testing.T) {
	f, err := spendSummaryInput{IncludeDemo: true}.toFilter()
	if err != nil {
		t.Fatalf("toFilter: %v", err)
	}
	if !reflect.DeepEqual(f.IncludeSources, []string{"demo"}) {
		t.Errorf("IncludeSources = %v; want [demo]", f.IncludeSources)
	}
}

// The synthetic-data banner exists to warn about demo seeds; it must
// stay suppressed exactly when the operator opted into them, however
// they spelled it.
func TestDemoOptedInDetectsBothSpellings(t *testing.T) {
	cases := []struct {
		name    string
		sources []string
		alias   bool
		want    bool
	}{
		{"neither", nil, false, false},
		{"alias", nil, true, true},
		{"named", []string{"demo"}, false, true},
		{"other source only", []string{"mcp-session"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := demoOptedIn(tc.sources, tc.alias); got != tc.want {
				t.Errorf("demoOptedIn(%v, %v) = %v; want %v", tc.sources, tc.alias, got, tc.want)
			}
		})
	}
}
