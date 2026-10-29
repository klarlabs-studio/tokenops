package proxy

import (
	"encoding/csv"
	"io"
	"net/http"
	"strings"
	"testing"
)

// GET /api/spend/export serves the same FOCUS CSV `tokenops spend export`
// writes, from the same capability (ADR 0010).
func TestSpendExportRouteServesFOCUSCSV(t *testing.T) {
	store, done := seedAnalyticsStore(t)
	defer done()
	base := "http://" + startAnalyticsProxy(t, store)

	resp, err := http.Get(base + "/api/spend/export?format=focus&since=72h")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, ".csv") {
		t.Errorf("Content-Disposition %q", cd)
	}
	if v := resp.Header.Get("X-Focus-Version"); v != "1.4" {
		t.Errorf("X-Focus-Version %q", v)
	}
	records, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("not CSV: %v\n%s", err, body)
	}
	if len(records) < 2 || records[0][0] != "BilledCost" {
		t.Fatalf("want a header and the seeded usage:\n%s", body)
	}
	for _, rec := range records[1:] {
		if rec[6] != "Usage" {
			t.Errorf("the seeded usage is metered, with no plan bound: %v", rec)
		}
	}

	old, err := http.Get(base + "/api/spend/export?version=1.2&since=72h")
	if err != nil {
		t.Fatal(err)
	}
	_ = old.Body.Close()
	if v := old.Header.Get("X-Focus-Version"); old.StatusCode != http.StatusOK || v != "1.2" {
		t.Errorf("?version=1.2: status %d, X-Focus-Version %q", old.StatusCode, v)
	}

	for _, bad := range []string{"/api/spend/export?format=parquet", "/api/spend/export?since=yesterday", "/api/spend/export?version=1.3"} {
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
