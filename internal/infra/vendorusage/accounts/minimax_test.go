package accounts

import (
	"context"
	"errors"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestMiniMaxTurnsRemainingIntoUsed(t *testing.T) {
	srv := serve(t, "/v1/token_plan/remains", "mk", fixture(t, "minimax"))
	defer srv.Close()
	got, err := MiniMax{BaseURL: srv.URL}.Read(context.Background(), "mk")
	if err != nil || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || w.UsedPct != 0 {
		t.Errorf("interval %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || w.UsedPct != 30 {
		t.Errorf("weekly %+v", w)
	}
	bad := serve(t, "/v1/token_plan/remains", "mk", `{"base_resp":{"status_code":1004,"status_msg":"login fail"}}`)
	defer bad.Close()
	if _, err := (MiniMax{BaseURL: bad.URL}).Read(context.Background(), "mk"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
}
