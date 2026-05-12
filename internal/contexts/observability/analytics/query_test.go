package analytics

import (
	"testing"
	"time"
)

func TestQueryParamsToFilterRFC3339(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC) }
	q := QueryParams{Since: "2026-05-01T00:00:00Z", Now: now}
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
	q := QueryParams{Since: "7d", Now: now}
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
	q := QueryParams{Since: "2h", Now: now}
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
	q := QueryParams{DefaultSince: time.Hour, Now: now}
	f, err := q.ToFilter()
	if err != nil {
		t.Fatal(err)
	}
	if !f.Since.Equal(now().Add(-time.Hour)) {
		t.Errorf("default since not applied: %v", f.Since)
	}
}

func TestQueryParamsToFilterUntilNonRFC3339Fails(t *testing.T) {
	q := QueryParams{Until: "not-a-time"}
	if _, err := q.ToFilter(); err == nil {
		t.Fatal("expected error for non-RFC3339 until")
	}
}

func TestResolveBucketAndGroup(t *testing.T) {
	if (QueryParams{Bucket: "day"}).ResolveBucket() != BucketDay {
		t.Errorf("Bucket day failed")
	}
	if (QueryParams{Bucket: ""}).ResolveBucket() != BucketHour {
		t.Errorf("Bucket default failed")
	}
	if (QueryParams{Group: "provider"}).ResolveGroup() != GroupProvider {
		t.Errorf("Group provider failed")
	}
	if (QueryParams{Group: "unknown"}).ResolveGroup() != GroupNone {
		t.Errorf("Group unknown should default to none")
	}
}
