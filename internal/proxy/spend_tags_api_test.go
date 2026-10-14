package proxy

import (
	"net/http"
	"testing"
)

// GET /api/spend/tags answers from the allocation capability: without
// rules configured nothing is tagged, and the key and window are checked.
func TestSpendTagsRoute(t *testing.T) {
	store, done := seedAnalyticsStore(t)
	defer done()
	base := "http://" + startAnalyticsProxy(t, store)

	got := getJSON(t, base+"/api/spend/tags?by=cost-center&since=72h")
	if got["key"] != "cost_center" || got["rules"] != float64(0) {
		t.Errorf("tags = %v", got)
	}
	if groups, ok := got["groups"].([]any); !ok || len(groups) != 0 {
		t.Errorf("groups = %v, want none without rules", got["groups"])
	}
	for _, k := range []string{"untagged", "tagged_share", "method", "since", "until"} {
		if _, ok := got[k]; !ok {
			t.Errorf("tags has no %q: %v", k, got)
		}
	}
	for _, bad := range []string{"/api/spend/tags?by=team", "/api/spend/tags?since=yesterday"} {
		resp, err := http.Get(base + bad)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", bad, resp.StatusCode)
		}
	}
}
