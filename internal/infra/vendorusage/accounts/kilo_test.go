package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const kiloPath = "/api/trpc/user.getCreditBlocks,kiloPass.getState"

func TestKiloReadsCreditAndPass(t *testing.T) {
	srv := serve(t, kiloPath, "kk", fixture(t, "kilo"))
	defer srv.Close()
	got, err := Kilo{BaseURL: srv.URL}.Read(context.Background(), "kk")
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 15) || !got.Subscription || len(got.Windows) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	// $27.50 of $49 base + $6 bonus.
	if w := got.Windows[0]; w.Name != "month" || !approx(w.UsedPct, 50) || !w.ResetsAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("pass window %+v", w)
	}
}

func TestKiloWithoutPassOrBlocks(t *testing.T) {
	srv := serve(t, kiloPath, "kk", `[{"result":{"data":{"json":{"creditBlocks":[],"totalBalance_mUsd":0}}}},{"result":{"data":{"json":{"subscription":null}}}}]`)
	defer srv.Close()
	got, err := Kilo{BaseURL: srv.URL}.Read(context.Background(), "kk")
	if err != nil || got.Subscription || !got.HasBalance || got.BalanceUSD != 0 {
		t.Fatalf("no pass = %+v, %v", got, err)
	}
	unknown := serve(t, kiloPath, "kk", `{"unexpected":true}`)
	defer unknown.Close()
	if _, err := (Kilo{BaseURL: unknown.URL}).Read(context.Background(), "kk"); err == nil {
		t.Error("an unknown shape was read")
	}
}

func TestKiloRefusesTheKey(t *testing.T) {
	srv := serve(t, kiloPath, "kk", fixture(t, "kilo"))
	defer srv.Close()
	if _, err := (Kilo{BaseURL: srv.URL}).Read(context.Background(), "bad"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
	// tRPC answers a batch with the refusal in the body.
	body := serve(t, kiloPath, "kk", `[{"error":{"json":{"message":"UNAUTHORIZED","data":{"code":"UNAUTHORIZED","httpStatus":401}}}},{"error":{"json":{"message":"UNAUTHORIZED","data":{"code":"UNAUTHORIZED"}}}}]`)
	defer body.Close()
	if _, err := (Kilo{BaseURL: body.URL}).Read(context.Background(), "kk"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refusal in the body = %v, want ErrAuth", err)
	}
}
