package analytics_test

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

func TestQueryParamsToFilterRFC3339(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC) }
	q := analytics.QueryParams{Since: "2026-05-01T00:00:00Z", Now: now}
	f, err := q.ToFilter()
	if err != nil {
		t.Fatal(err)
	}
	if !f.Since.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Since = %v", f.Since)
	}
}

func TestQueryParamsToFilterDays(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC) }
	q := analytics.QueryParams{Since: "7d", Now: now}
	f, err := q.ToFilter()
	if err != nil {
		t.Fatal(err)
	}
	want := now().Add(-7 * 24 * time.Hour)
	if !f.Since.Equal(want) {
		t.Errorf("Since = %v, want %v", f.Since, want)
	}
}

func TestQueryParamsToFilterDuration(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC) }
	q := analytics.QueryParams{Since: "2h", Now: now}
	f, err := q.ToFilter()
	if err != nil {
		t.Fatal(err)
	}
	if !f.Since.Equal(now().Add(-2 * time.Hour)) {
		t.Errorf("Since = %v", f.Since)
	}
}

func TestQueryParamsToFilterDefaultSince(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC) }
	q := analytics.QueryParams{DefaultSince: time.Hour, Now: now}
	f, err := q.ToFilter()
	if err != nil {
		t.Fatal(err)
	}
	if !f.Since.Equal(now().Add(-time.Hour)) {
		t.Errorf("default since not applied: %v", f.Since)
	}
}

func TestQueryParamsToFilterUntilNonRFC3339Fails(t *testing.T) {
	q := analytics.QueryParams{Until: "not-a-time"}
	if _, err := q.ToFilter(); err == nil {
		t.Fatal("expected error for non-RFC3339 until")
	}
}

func TestResolveBucketAndGroup(t *testing.T) {
	if (analytics.QueryParams{Bucket: "day"}).ResolveBucket() != analytics.BucketDay {
		t.Errorf("Bucket day failed")
	}
	if (analytics.QueryParams{Bucket: ""}).ResolveBucket() != analytics.BucketHour {
		t.Errorf("Bucket default failed")
	}
	if (analytics.QueryParams{Group: "provider"}).ResolveGroup() != analytics.GroupProvider {
		t.Errorf("Group provider failed")
	}
	if (analytics.QueryParams{Group: "unknown"}).ResolveGroup() != analytics.GroupNone {
		t.Errorf("Group unknown should default to none")
	}
}
