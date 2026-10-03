package proxy

import (
	"net/http"
	"testing"
)

func TestSpendingRoutesAnswer(t *testing.T) {
	store, done := seedAnalyticsStore(t)
	defer done()
	base := "http://" + startAnalyticsProxy(t, store)

	top := getJSON(t, base+"/api/spend/top?by=workflow&top=1")
	if top["by"] != "workflow" || len(top["top"].([]any)) != 1 {
		t.Errorf("top = %v", top)
	}
	burn := getJSON(t, base+"/api/spend/burn-rate?hours=6")
	if burn["hours"] != float64(6) || burn["tokens"] != float64(3600) {
		t.Errorf("burn = %v", burn)
	}
	if card := getJSON(t, base+"/api/scorecard?since_days=7"); card == nil {
		t.Error("no scorecard")
	}
	pricing := getJSON(t, base+"/api/pricing?provider=anthropic&limit=2")
	if rates, _ := pricing["rates"].([]any); len(rates) == 0 || len(rates) > 2 {
		t.Errorf("pricing = %v", pricing)
	}
	for _, bad := range []string{"/api/spend/top?by=colour", "/api/spend/burn-rate?hours=x", "/api/pricing?limit=x"} {
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

func TestDecisionRoutesAnswer(t *testing.T) {
	store, done := seedAnalyticsStore(t)
	defer done()
	base := "http://" + startAnalyticsProxy(t, store)

	if p := getJSON(t, base+"/api/routing/proposals"); p["pending"] == nil {
		t.Errorf("proposals = %v", p)
	}
	resp, err := http.Get(base + "/api/decisions/decision:unknown")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown decision = %d, want 404", resp.StatusCode)
	}
}
