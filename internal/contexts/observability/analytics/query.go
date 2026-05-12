package analytics

import (
	"fmt"
	"strings"
	"time"
)

// QueryParams is the value object every HTTP / MCP / CLI adapter
// constructs from raw protocol input (query strings, JSON args, flags)
// before calling Aggregator. The analytics domain accepts a Filter, but
// only QueryParams enforces parse rules in one place — adapters never
// re-implement them.
//
// Each field accepts the raw textual form the operator typed; ToFilter
// validates and converts. Empty fields are skipped.
type QueryParams struct {
	Since      string
	Until      string
	Provider   string
	Model      string
	WorkflowID string
	AgentID    string
	Bucket     string
	Group      string

	// DefaultSince is the lookback window to apply when Since is empty.
	// Zero disables the default (Filter.Since stays zero).
	DefaultSince time.Duration
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

// ToFilter materialises the analytics.Filter applying parse rules:
//
//   - Since accepts RFC3339, "Nd" (day count), or any time.ParseDuration
//     string. Empty falls back to DefaultSince when set.
//   - Until accepts RFC3339 only (relative timestamps make no sense at
//     a window boundary).
//
// Returned errors carry the originating field name so the HTTP adapter
// can surface them as 400-bad-request.
func (q QueryParams) ToFilter() (Filter, error) {
	if q.Now == nil {
		q.Now = time.Now
	}
	f := Filter{
		Provider:   q.Provider,
		Model:      q.Model,
		WorkflowID: q.WorkflowID,
		AgentID:    q.AgentID,
	}
	if q.Since != "" {
		t, err := parseTimeOrDuration(q.Since, q.Now)
		if err != nil {
			return f, fmt.Errorf("since: %w", err)
		}
		f.Since = t
	} else if q.DefaultSince > 0 {
		f.Since = q.Now().Add(-q.DefaultSince)
	}
	if q.Until != "" {
		t, err := time.Parse(time.RFC3339, q.Until)
		if err != nil {
			return f, fmt.Errorf("until: %w", err)
		}
		f.Until = t
	}
	return f, nil
}

// ResolveBucket parses the Bucket field. Unknown values default to
// BucketHour so callers always receive a valid value.
func (q QueryParams) ResolveBucket() Bucket {
	switch strings.ToLower(q.Bucket) {
	case "day":
		return BucketDay
	default:
		return BucketHour
	}
}

// ResolveGroup parses the Group field. Unknown values default to
// GroupNone.
func (q QueryParams) ResolveGroup() Group {
	switch strings.ToLower(q.Group) {
	case "provider":
		return GroupProvider
	case "workflow":
		return GroupWorkflow
	case "agent":
		return GroupAgent
	case "model":
		return GroupModel
	default:
		return GroupNone
	}
}

func parseTimeOrDuration(s string, now func() time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if strings.HasSuffix(s, "d") {
		var days int
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil && days > 0 {
			return now().Add(-time.Duration(days) * 24 * time.Hour), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, err
	}
	return now().Add(-d), nil
}
