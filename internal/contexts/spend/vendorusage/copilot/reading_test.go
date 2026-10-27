package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// orgSeatResponse is the org-seat shape reported in openusage issue #1094
// (anonymised there; trimmed here): every bucket unlimited with
// entitlement 0, credits used on premium_interactions.
const orgSeatResponse = `{
  "login": "member",
  "access_type_sku": "copilot_for_business_seat_quota",
  "copilot_plan": "business",
  "token_based_billing": true,
  "quota_reset_date": "2026-09-01",
  "timestamp_utc": "2026-08-12T09:00:00Z",
  "quota_snapshots": {
    "chat": {"entitlement":0,"unlimited":true,"token_based_billing":true,"credits_used":0,"percent_remaining":100.0},
    "completions": {"entitlement":0,"unlimited":true,"token_based_billing":true,"credits_used":0,"percent_remaining":100.0},
    "premium_interactions": {"entitlement":0,"unlimited":true,"token_based_billing":true,"credits_used":2111,"overage_permitted":true,"percent_remaining":100.0}
  }
}`

// creditPlanResponse is a monthly Pro+ seat on usage-based billing: its
// month's 7,000 AI Credits on premium_interactions. The credit fields are
// as the org seat reports them; that an individual seat reports its
// allowance as the entitlement is an assumption, not an observation.
const creditPlanResponse = `{
  "login": "solo",
  "copilot_plan": "individual_pro_plus",
  "token_based_billing": true,
  "quota_reset_date": "2026-11-01",
  "timestamp_utc": "2026-10-20T09:00:00Z",
  "quota_snapshots": {
    "chat": {"entitlement":0,"unlimited":true,"percent_remaining":100.0},
    "premium_interactions": {"entitlement":7000,"unlimited":false,"token_based_billing":true,"credits_used":5250,"percent_remaining":25.0}
  }
}`

func decode(t *testing.T, body string) *UserResponse {
	t.Helper()
	var u UserResponse
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatal(err)
	}
	return &u
}

func TestReadingOfACreditBilledPlan(t *testing.T) {
	r, err := decode(t, creditPlanResponse).Reading()
	if err != nil {
		t.Fatal(err)
	}
	if r.Unmetered || len(r.Meters) != 1 {
		t.Fatalf("reading = %+v; want one metered bucket", r)
	}
	m := r.Meters[0]
	if m.Name != "month (AI credits)" || m.UsedPct != 75 || !m.Credits || m.CreditsUsed != 5250 {
		t.Errorf("meter = %+v; want month (AI credits) 75%% used, 5250 credits", m)
	}
	if want := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC); !r.ResetsAt.Equal(want) {
		t.Errorf("resets %v, want %v", r.ResetsAt, want)
	}
}

// Without a share, a credit-billed bucket's credits over its allowance
// stand in for it.
func TestCreditsStandInForAMissingShare(t *testing.T) {
	u := decode(t, `{"token_based_billing":true,"quota_snapshots":{"premium_interactions":{"entitlement":1500,"credits_used":300}}}`)
	r, err := u.Reading()
	if err != nil || len(r.Meters) != 1 || r.Meters[0].UsedPct != 20 {
		t.Fatalf("reading = %+v, %v; want 20%% used", r, err)
	}
}

// An annual plan still on premium requests keeps that name.
func TestAnAnnualPlanKeepsPremiumRequests(t *testing.T) {
	r, err := decode(t, sampleResponse).Reading()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Meters) != 2 || r.Meters[0].Name != "month (chat)" || r.Meters[1].Name != "month (premium requests)" {
		t.Errorf("meters = %+v; want chat (30%%) then premium requests (0%%)", r.Meters)
	}
}

// An org seat has no personal allowance: unmetered, with its credits
// used, never a meter at 0%.
func TestAnOrgSeatIsUnmetered(t *testing.T) {
	r, err := decode(t, orgSeatResponse).Reading()
	if err != nil {
		t.Fatal(err)
	}
	if !r.Unmetered || len(r.Meters) != 0 {
		t.Errorf("reading = %+v; want an unmetered seat with no meters", r)
	}
	if !r.HasCredits || r.CreditsUsed != 2111 {
		t.Errorf("credits = %v (%v); want 2111", r.CreditsUsed, r.HasCredits)
	}
}

func TestUnrecognisedShapesAreErrors(t *testing.T) {
	for name, body := range map[string]string{
		"no buckets":         `{"login":"x"}`,
		"capped, no share":   `{"quota_snapshots":{"premium_interactions":{"entitlement":300}}}`,
		"credits, no credit": `{"token_based_billing":true,"quota_snapshots":{"premium_interactions":{"entitlement":1500}}}`,
	} {
		if _, err := decode(t, body).Reading(); !errors.Is(err, ErrUnrecognisedShape) {
			t.Errorf("%s: err = %v, want ErrUnrecognisedShape", name, err)
		}
	}
}

// A record the poller cannot read is a failed poll with the reason, never
// a successful one that stored nothing.
func TestPollerFailsOnAnUnrecognisedShape(t *testing.T) {
	rec := freshness.NewRecorder()
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		Health: rec, OAuthToken: "tok", Interval: time.Hour,
		NewClient: sourceFor(fakeSource{body: `{"login":"x","timestamp_utc":"2026-10-20T09:00:00Z"}`}),
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	p.scan(context.Background())
	got := rec.Poll()
	if got.LastError == nil || !got.LastSuccessAt.IsZero() {
		t.Errorf("health = %+v; want a failure and no success", got)
	}
	if bus.PublishedCount() != 0 {
		t.Errorf("stored %d envelopes from an unreadable record", bus.PublishedCount())
	}
}

func planEnvelope(t *testing.T, bus *captureBus) *eventschema.Envelope {
	t.Helper()
	for _, e := range bus.envelopes {
		if e.Attributes["snapshot"] == PlanSnapshot {
			return e
		}
	}
	t.Fatal("no plan envelope stored")
	return nil
}

func pollOnce(t *testing.T, body string) *captureBus {
	t.Helper()
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		OAuthToken: "tok", Interval: time.Hour, NewClient: sourceFor(fakeSource{body: body}),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	p.scan(context.Background())
	return bus
}

// The plan envelope carries the month's credits as a window in the shape
// headroom reads for vendor accounts.
func TestPlanEnvelopeCarriesTheCreditsWindow(t *testing.T) {
	e := planEnvelope(t, pollOnce(t, creditPlanResponse))
	want := map[string]string{
		"seat": "metered", "plan_type": "individual_pro_plus",
		"window_0_name": "month (AI credits)", "window_0_used_pct": "75.00",
		"window_0_reset_at": "2026-11-01T00:00:00Z",
		"used_credits":      "5250", "used_credits_unit": "AI credits",
	}
	for k, v := range want {
		if e.Attributes[k] != v {
			t.Errorf("%s = %q, want %q", k, e.Attributes[k], v)
		}
	}
	if pe, ok := e.Payload.(*eventschema.PromptEvent); !ok || pe.Provider != eventschema.ProviderGitHub {
		t.Errorf("payload = %+v; want provider github", e.Payload)
	}
}

func TestPlanEnvelopeOfAnOrgSeatHasNoWindow(t *testing.T) {
	e := planEnvelope(t, pollOnce(t, orgSeatResponse))
	if e.Attributes["seat"] != "unmetered" || e.Attributes["used_credits"] != "2111" {
		t.Errorf("attrs = %v; want an unmetered seat with 2111 credits used", e.Attributes)
	}
	if _, ok := e.Attributes["window_0_used_pct"]; ok {
		t.Error("an unmetered seat must carry no window")
	}
}

// A bucket that reports no share stores no percent_remaining, so it is
// never read as "0% left".
func TestSnapshotWithoutAShareStoresNone(t *testing.T) {
	bus := pollOnce(t, `{"timestamp_utc":"2026-10-20T09:00:00Z","quota_snapshots":{"chat":{"entitlement":0,"unlimited":true}}}`)
	for _, e := range bus.envelopes {
		if _, ok := e.Attributes["percent_remaining"]; ok {
			t.Errorf("%s stored percent_remaining it was never given", e.Attributes["snapshot"])
		}
	}
}
