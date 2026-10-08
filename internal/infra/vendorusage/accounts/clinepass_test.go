package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const clinePassPath = "/api/v1/users/me/plan/usage-limits"

func TestClinePassReadsTheThreeWindows(t *testing.T) {
	srv := serve(t, clinePassPath, "ck", fixture(t, "clinepass"))
	defer srv.Close()
	got, err := ClinePass{BaseURL: srv.URL}.Read(context.Background(), "ck")
	if err != nil || !got.Subscription || len(got.Windows) != 3 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 42.5) || w.Duration != 5*time.Hour ||
		!w.ResetsAt.Equal(time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)) {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 18) {
		t.Errorf("week %+v", w)
	}
	if w := got.Windows[2]; w.Name != "month" || !approx(w.UsedPct, 7.25) || !w.ResetsAt.IsZero() {
		t.Errorf("month %+v", w)
	}
}

func TestClinePassRefusedAndUnknownAnswers(t *testing.T) {
	srv := serve(t, clinePassPath, "ck", `{"success":true,"data":{"limits":[]}}`)
	defer srv.Close()
	if _, err := (ClinePass{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
	if got, err := (ClinePass{BaseURL: srv.URL}).Read(context.Background(), "ck"); err != nil || !got.Empty() || got.Subscription {
		t.Errorf("no limits = %+v, %v", got, err)
	}
	failed := serve(t, clinePassPath, "ck", `{"success":false}`)
	defer failed.Close()
	if _, err := (ClinePass{BaseURL: failed.URL}).Read(context.Background(), "ck"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("an unsuccessful answer = %v", err)
	}
	over := serve(t, clinePassPath, "ck", `{"success":true,"data":{"limits":[{"type":"weekly","percentUsed":140}]}}`)
	defer over.Close()
	if got, err := (ClinePass{BaseURL: over.URL}).Read(context.Background(), "ck"); err != nil || got.Windows[0].UsedPct != 100 {
		t.Errorf("over the limit = %+v, %v", got, err)
	}
}
