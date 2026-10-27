package accounts

import (
	"context"
	"testing"
	"time"
)

func TestKimiReadsRatios(t *testing.T) {
	srv := serve(t, "/coding/v1/usages", "kk", fixture(t, "kimi"))
	defer srv.Close()
	got, err := Kimi{BaseURL: srv.URL}.Read(context.Background(), "kk")
	if err != nil || len(got.Windows) != 3 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 30) || w.ResetsAt.IsZero() {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 20) {
		t.Errorf("week %+v", w)
	}
}

func TestKimiReadsMonthCodeShareAsSubWindow(t *testing.T) {
	srv := serve(t, "/coding/v1/usages", "kk", fixture(t, "kimi-month-code"))
	defer srv.Close()
	got, err := Kimi{BaseURL: srv.URL}.Read(context.Background(), "kk")
	if err != nil || len(got.Windows) != 3 {
		t.Fatalf("got %+v, %v", got, err)
	}
	w := got.Windows[2]
	if w.Name != "month_code" || !approx(w.UsedPct, 12.5) || w.Duration != 30*24*time.Hour || w.ResetsAt.IsZero() {
		t.Errorf("month_code %+v", w)
	}
}

func TestKimiReadsLegacyCountsWhenRatiosAbsent(t *testing.T) {
	srv := serve(t, "/coding/v1/usages", "kk", fixture(t, "kimi-legacy"))
	defer srv.Close()
	got, err := Kimi{BaseURL: srv.URL}.Read(context.Background(), "kk")
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || !approx(w.UsedPct, 40) || w.Duration != 5*time.Hour || w.ResetsAt.IsZero() {
		t.Errorf("5h %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 25) || w.Duration != 7*24*time.Hour {
		t.Errorf("week %+v", w)
	}
}

func TestKimiRatioWindowWinsOverLegacyOfSameName(t *testing.T) {
	body := `{"usages":{"limit_5h":{"used_ratio":0.3,"reset_time":"2026-09-11T18:00:00Z"}},
		"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},"detail":{"limit":"100","used":"90"}}]}`
	srv := serve(t, "/coding/v1/usages", "kk", body)
	defer srv.Close()
	got, err := Kimi{BaseURL: srv.URL}.Read(context.Background(), "kk")
	if err != nil || len(got.Windows) != 1 || !approx(got.Windows[0].UsedPct, 30) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestKimiMissingWeeklyWindowIsNotAnError(t *testing.T) {
	body := `{"usages":{"limit_5h":{"used_ratio":0.1,"reset_time":"2026-09-11T18:00:00Z"}}}`
	srv := serve(t, "/coding/v1/usages", "kk", body)
	defer srv.Close()
	got, err := Kimi{BaseURL: srv.URL}.Read(context.Background(), "kk")
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Name != "5h" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestKimiNoWindowIsAnError(t *testing.T) {
	for _, body := range []string{`{"usages":{}}`, `{}`, `{"usages":{"limit_5h":{"used_ratio":"x"}}}`,
		`{"limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_FORTNIGHT"},"detail":{"limit":"1","used":"0"}}]}`} {
		srv := serve(t, "/coding/v1/usages", "kk", body)
		got, err := Kimi{BaseURL: srv.URL}.Read(context.Background(), "kk")
		srv.Close()
		if err == nil || !got.Empty() {
			t.Errorf("body %s: got %+v, %v; want an error", body, got, err)
		}
	}
}
