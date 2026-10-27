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

// Plans sold since 2026-07-30 report the 5-hour and weekly windows as
// CREDIT_LIMIT rows: the same unit and number, with the window's credit
// allowance in usage and what is spent in currentValue. A row may leave
// percentage out (the weekly one here) and a window not started yet has no
// reset time.
func TestZAIReadsTheCreditWindows(t *testing.T) {
	srv := serveRaw(t, "/api/monitor/usage/quota/limit", "zk", fixture(t, "zhipuai"))
	defer srv.Close()
	got, err := ZAI{BaseURL: srv.URL}.Read(context.Background(), "zk")
	if err != nil || !got.Subscription || len(got.Windows) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if w := got.Windows[0]; w.Name != "5h" || w.UsedPct != 25 || !w.ResetsAt.IsZero() {
		t.Errorf("5h window %+v", w)
	}
	if w := got.Windows[1]; w.Name != "week" || !approx(w.UsedPct, 40) || !w.ResetsAt.Equal(time.UnixMilli(1792000000000)) {
		t.Errorf("week window %+v", w)
	}
}

// An answer TokenOps cannot read is a failure with its reason, never a
// successful reading with no windows.
func TestZAIRefusesAnAnswerWithNoWindow(t *testing.T) {
	for name, body := range map[string]string{
		"no limits":      `{"success":true,"code":200,"data":{}}`,
		"empty limits":   `{"success":true,"code":200,"data":{"limits":[]}}`,
		"unknown type":   `{"success":true,"code":200,"data":{"limits":[{"type":"QUOTA_V3","unit":3,"number":5,"percentage":10}]}}`,
		"no percentage":  `{"success":true,"code":200,"data":{"limits":[{"type":"CREDIT_LIMIT","unit":3,"number":5}]}}`,
		"only the tools": `{"success":true,"code":200,"data":{"limits":[{"type":"TIME_LIMIT","unit":5,"number":1,"percentage":3}]}}`,
	} {
		srv := serveRaw(t, "/api/monitor/usage/quota/limit", "zk", body)
		got, err := ZAI{BaseURL: srv.URL}.Read(context.Background(), "zk")
		srv.Close()
		if err == nil || errors.Is(err, usage.ErrAuth) {
			t.Errorf("%s: got %+v, %v; want an unrecognised-answer error", name, got, err)
		}
	}
}

// Zhipu AI, z.ai's mainland-China platform, serves the same quota API on
// open.bigmodel.cn; its keys are its own, read and reported as zhipuai.
func TestZhipuAIReadsTheSameQuotaAPI(t *testing.T) {
	r := readerZhipuAI()
	if r.Endpoint() != "zhipuai" || r.Provider() != "zhipuai" || r.Source() != "zhipuai-account" {
		t.Fatalf("reader %s/%s/%s", r.Endpoint(), r.Provider(), r.Source())
	}
	if got := (ZhipuAI{}).baseURL(); got != "https://open.bigmodel.cn" {
		t.Errorf("default host %q", got)
	}
	srv := serveRaw(t, "/api/monitor/usage/quota/limit", "bk", fixture(t, "zhipuai"))
	defer srv.Close()
	got, err := ZhipuAI{BaseURL: srv.URL}.Read(context.Background(), "bk")
	if err != nil || len(got.Windows) != 2 || got.Windows[0].Name != "5h" {
		t.Fatalf("got %+v, %v", got, err)
	}
}
