package accounts

import (
	"context"
	"testing"
)

func TestChutesCapsAndNoSubscription(t *testing.T) {
	srv := serve(t, "/users/me/subscription_usage", "ck", fixture(t, "chutes"))
	defer srv.Close()
	got, err := Chutes{BaseURL: srv.URL}.Read(context.Background(), "ck")
	if err != nil || len(got.Windows) != 1 || got.Windows[0].Name != "4h" || !approx(got.Windows[0].UsedPct, 25) || got.Windows[0].ResetsAt.IsZero() {
		t.Fatalf("got %+v, %v", got, err)
	}
	none := serve(t, "/users/me/subscription_usage", "ck", `{"subscription":false}`)
	defer none.Close()
	if got, err := (Chutes{BaseURL: none.URL}).Read(context.Background(), "ck"); err != nil || !got.Empty() {
		t.Errorf("no subscription = %+v, %v", got, err)
	}
}
