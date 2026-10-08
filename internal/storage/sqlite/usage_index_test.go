package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// TestUsageQueriesAreCovered pins the spend rollups to events_usage_idx as
// a covering index. Reading the table instead fetches each event's payload
// and attributes, which made a month's summary take seconds on a real store.
// An expression in analytics.go that drifts from the one migration 5
// indexes fails here rather than silently going slow.
func TestUsageQueriesAreCovered(t *testing.T) {
	s := newTestStore(t)
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	filters := map[string]analytics.Filter{
		"provider window": {Provider: "anthropic", Since: since},
		"bounded window":  {Provider: "anthropic", Since: since, Until: since.Add(24 * time.Hour)},
		"all providers":   {Since: since},
		"model":           {Provider: "openai", Model: "gpt-5.5", Since: since},
		"re-admitted":     {Since: since, IncludeSources: []string{"mcp-session"}},
	}
	for name, f := range filters {
		for _, g := range []analytics.Group{analytics.GroupNone, analytics.GroupModel, analytics.GroupProvider} {
			q, args := usageScanQuery(f, g)
			plan := explain(t, s, q, args)
			if !strings.Contains(plan, "COVERING INDEX events_usage_idx") {
				t.Errorf("%s / group %q reads the table:\n%s", name, g, plan)
			}
		}
	}
}

// TestUsageScanWalksTheIndexTheOldQueriesDid pins the order the single
// read visits events in. Its cost sums reproduce SQLite's, which depend on
// the order they add in: for every filter and grouping, the read must walk
// the index the statement whose cost sums it replaces walked — the bucket
// rows' for BucketUsage, the totals' for WindowUsage. (The pricing groups
// sum integers only, so their order of addition does not matter.)
func TestUsageScanWalksTheIndexTheOldQueriesDid(t *testing.T) {
	s := newTestStore(t)
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	filters := map[string]analytics.Filter{
		"window":   {Since: since},
		"provider": {Provider: "anthropic", Since: since, Until: since.Add(24 * time.Hour)},
		"model":    {Provider: "openai", Model: "gpt-5.5", Since: since},
		"workflow": {WorkflowID: "wf", Since: since},
		"agent":    {AgentID: "ag", Since: since},
		"no since": {Provider: "anthropic"},
	}
	groups := []analytics.Group{analytics.GroupNone, analytics.GroupModel, analytics.GroupProvider, analytics.GroupWorkflow, analytics.GroupAgent}
	for name, f := range filters {
		for _, g := range groups {
			q, args := usageScanQuery(f, g)
			bq, bargs := oracleUsageBucketsQuery(f, 3600, g)
			if got, want := scanStep(explain(t, s, q, args)), scanStep(explain(t, s, bq, bargs)); got != want {
				t.Errorf("%s / group %q: single read %q, bucket rows %q", name, g, got, want)
			}
		}
		q, args := usageScanQuery(f, analytics.GroupNone)
		tq, targs := oracleUsageTotalsQuery(f)
		if got, want := scanStep(explain(t, s, q, args)), scanStep(explain(t, s, tq, targs)); got != want {
			t.Errorf("%s: single read %q, totals %q", name, got, want)
		}
	}
}

// scanStep is a plan's table access with "COVERING " dropped: which index
// it walks and how, whatever else it reads.
func scanStep(plan string) string {
	first, _, _ := strings.Cut(plan, "\n")
	return strings.Replace(first, "COVERING ", "", 1)
}

// explain is SQLite's query plan for q, one detail per line.
func explain(t *testing.T, s *Store, q string, args []any) string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("explain scan: %v", err)
		}
		lines = append(lines, detail)
	}
	return strings.Join(lines, "\n")
}
