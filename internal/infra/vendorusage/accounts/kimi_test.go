package accounts

import (
	"context"
	"testing"
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
