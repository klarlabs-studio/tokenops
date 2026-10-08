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
		queries := map[string]func() (string, []any){
			"usage totals":       func() (string, []any) { return usageTotalsQuery(f) },
			"buckets":            func() (string, []any) { return usageBucketsQuery(f, 86400, analytics.GroupNone) },
			"buckets by model":   func() (string, []any) { return usageBucketsQuery(f, 86400, analytics.GroupModel) },
			"buckets by provid.": func() (string, []any) { return usageBucketsQuery(f, 3600, analytics.GroupProvider) },
			"bucket recompute": func() (string, []any) {
				return bucketPricingQuery(f, 86400, analytics.GroupModel, analytics.UncostedMetered)
			},
			"bucket plan value": func() (string, []any) {
				return bucketPricingQuery(f, 86400, analytics.GroupNone, analytics.PlanCovered)
			},
			"daily recompute":  func() (string, []any) { return dailyPricingQuery(f, analytics.UncostedMetered) },
			"daily plan value": func() (string, []any) { return dailyPricingQuery(f, analytics.PlanCovered) },
		}
		for qname, build := range queries {
			q, args := build()
			plan := explain(t, s, q, args)
			if !strings.Contains(plan, "COVERING INDEX events_usage_idx") {
				t.Errorf("%s / %s reads the table:\n%s", name, qname, plan)
			}
		}
	}
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
