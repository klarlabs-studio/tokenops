package daemon

import (
	"context"
	"testing"
	"time"
)

// One reading serves a minute of asks from both budget runtimes.
func TestBillsAreReadAtMostOnceAMinute(t *testing.T) {
	reads := 0
	v := &vendorBills{read: func(context.Context, time.Time) map[string]float64 {
		reads++
		return map[string]float64{"fireworks": float64(100 + reads)}
	}}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if usd, ok := v.Bill(ctx, "fireworks", now); !ok || usd != 101 {
		t.Fatalf("first = %v, %v", usd, ok)
	}
	if _, ok := v.Bill(ctx, "anthropic", now.Add(30*time.Second)); ok {
		t.Errorf("a provider without a bill reported one")
	}
	if usd, _ := v.Bill(ctx, "fireworks", now.Add(59*time.Second)); usd != 101 || reads != 1 {
		t.Errorf("within the minute: %v after %d reads; want the cached 101", usd, reads)
	}
	if usd, _ := v.Bill(ctx, "fireworks", now.Add(time.Minute)); usd != 102 || reads != 2 {
		t.Errorf("after the minute: %v after %d reads; want a fresh 102", usd, reads)
	}
}
