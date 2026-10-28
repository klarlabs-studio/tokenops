package codexappserver

import (
	"context"
	"testing"
	"time"
)

// twoLimits is the account/rateLimits/read result shape codex-rs's
// app server sends (app-server/src/request_processors/account_processor.rs):
// rateLimits is the codex bucket, rateLimitsByLimitId every bucket.
const twoLimits = `"result":{"rateLimits":{"limitId":"codex","limitName":null,"planType":"prolite","primary":{"usedPercent":40,"windowDurationMins":300,"resetsAt":1791600000},"secondary":{"usedPercent":12,"windowDurationMins":10080,"resetsAt":1791607287},"rateLimitReachedType":null},` +
	`"rateLimitsByLimitId":{"codex":{"limitId":"codex","limitName":null,"planType":"prolite","primary":{"usedPercent":40,"windowDurationMins":300,"resetsAt":1791600000},"secondary":{"usedPercent":12,"windowDurationMins":10080,"resetsAt":1791607287},"rateLimitReachedType":null},` +
	`"codex_secondary":{"limitId":"codex_secondary","limitName":"Example Model","planType":"prolite","primary":{"usedPercent":7,"windowDurationMins":10080,"resetsAt":1791700000},"secondary":null,"rateLimitReachedType":"rate_limit_reached"}}}`

func TestReadAllReturnsEveryLimitBucketOnce(t *testing.T) {
	dial, _ := fakeServer(t, twoLimits)
	got, err := ReadAll(context.Background(), dial)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d buckets, want the codex bucket and codex_secondary (codex once): %+v", len(got), got)
	}
	if got[0].LimitID != "codex" || got[0].Primary == nil || got[0].Primary.UsedPercent != 40 {
		t.Errorf("default bucket = %+v, want codex at 40%%", got[0])
	}
	spark := got[1]
	if spark.LimitID != "codex_secondary" || spark.LimitName != "Example Model" || spark.Primary == nil || spark.Primary.UsedPercent != 7 {
		t.Errorf("second bucket = %+v, want codex_secondary Example Model at 7%%", spark)
	}
	if spark.RateLimitReachedType != "rate_limit_reached" {
		t.Errorf("reached type = %q, want rate_limit_reached", spark.RateLimitReachedType)
	}
}

// Read keeps its contract: the codex bucket, as before.
func TestReadStillReturnsTheCodexBucket(t *testing.T) {
	dial, _ := fakeServer(t, twoLimits)
	s, err := Read(context.Background(), dial)
	if err != nil || s.LimitID != "codex" || s.Primary == nil || s.Primary.UsedPercent != 40 {
		t.Fatalf("read = %+v, %v", s, err)
	}
}

func TestEnvelopeNamesANonDefaultLimitAndLeavesTheDefaultAlone(t *testing.T) {
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	dial, _ := fakeServer(t, twoLimits)
	buckets, err := ReadAll(context.Background(), dial)
	if err != nil {
		t.Fatal(err)
	}
	def := Envelope(buckets[0], at).Attributes
	if _, ok := def["limit_id"]; ok {
		t.Errorf("default envelope carries limit_id %q", def["limit_id"])
	}
	spark := Envelope(buckets[1], at).Attributes
	if spark["limit_id"] != "codex_secondary" || spark["limit_name"] != "Example Model" || spark["primary_used_pct"] != "7.00" {
		t.Errorf("spark envelope = %+v", spark)
	}
	if spark["limit_reached"] != "rate_limit_reached" {
		t.Errorf("spark limit_reached = %q", spark["limit_reached"])
	}
}

// Each limit is stored once per change, so two limits make two readings
// on the first scan and none while nothing changes.
func TestPollerStoresEachLimitOnce(t *testing.T) {
	at := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	dial, _ := fakeServer(t, twoLimits)
	bus := &recordingBus{}
	p := NewPoller(bus, PollerOptions{Dial: dial, Now: func() time.Time { return at }})
	p.Scan(context.Background())
	p.Scan(context.Background())
	if len(bus.got) != 2 {
		t.Fatalf("stored %d readings, want one per limit (2)", len(bus.got))
	}
	if bus.got[0].ID == bus.got[1].ID {
		t.Errorf("both limits share envelope ID %s", bus.got[0].ID)
	}
}
