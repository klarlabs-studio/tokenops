package accounts

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestZAIReadsTheTokenWindows(t *testing.T) {
	srv := serveRaw(t, "/api/monitor/usage/quota/limit", "zk", fixture(t, "zai"))
	defer srv.Close()
	got, err := ZAI{BaseURL: srv.URL}.Read(context.Background(), "zk")
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || w.UsedPct != 37 || !w.ResetsAt.Equal(time.UnixMilli(1791000000000)) {
		t.Errorf("5h window %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || w.UsedPct != 12 {
		t.Errorf("week window %+v", w)
	}
	srv2 := serveRaw(t, "/api/monitor/usage/quota/limit", "zk", `{"success":false,"msg":"bad plan","code":500}`)
	defer srv2.Close()
	if _, err := (ZAI{BaseURL: srv2.URL}).Read(context.Background(), "zk"); err == nil || errors.Is(err, usage.ErrAuth) {
		t.Errorf("an unsuccessful answer = %v", err)
	}
	// As seen live: a 200 whose body refuses the key.
	refused := serveRaw(t, "/api/monitor/usage/quota/limit", "zk", `{"code":1001,"msg":"Authentication parameter not received in Header","success":false}`)
	defer refused.Close()
	if _, err := (ZAI{BaseURL: refused.URL}).Read(context.Background(), "zk"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("a refused key = %v, want ErrAuth", err)
	}
}
