package accounts

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const qoderPath = "/api/v2/me/usages/big_model_credits"

func TestQoderReadsCredits(t *testing.T) {
	srv := newWebServer(t, "sid=1", http.StatusUnauthorized, routesOf(t, "qoder"))
	got, err := Qoder{Sites: []string{srv.URL}}.Read(context.Background(), "sid=1")
	if err != nil || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "credits" || w.UsedPct != 25 || !w.ResetsAt.Equal(time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("credits %+v", w)
	}
}

// Team shared credits are added; snake case is the legacy shape; a
// session of the China site is read there after the global one refuses.
func TestQoderSharedLegacyAndChinaSite(t *testing.T) {
	global := newWebServer(t, "never", http.StatusUnauthorized, nil)
	cn := newWebServer(t, "sid=1", http.StatusUnauthorized, map[string]string{qoderPath: `{
		"total_quota":{"quota_summary":{"used_value":1500,"limit_value":1500,"remaining_value":0,"usage_percentage":100}},
		"shared_quota":{"quota_summary":{"used_value":200,"limit_value":1000,"remaining_value":800,"usage_percentage":20}}}`})
	got, err := Qoder{Sites: []string{global.URL, cn.URL}}.Read(context.Background(), "sid=1")
	if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 68) || !got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestQoderRefusedAndUnknown(t *testing.T) {
	a, b := newWebServer(t, "sid=1", http.StatusForbidden, nil), newWebServer(t, "sid=1", http.StatusFound, nil)
	if _, err := (Qoder{Sites: []string{a.URL, b.URL}}).Read(context.Background(), "sid=old"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("refused = %v", err)
	}
	odd := newWebServer(t, "", 0, map[string]string{qoderPath: `{"status":"active"}`})
	if _, err := (Qoder{Sites: []string{odd.URL}}).Read(context.Background(), "sid=1"); err == nil {
		t.Error("an unknown shape was read")
	}
}
