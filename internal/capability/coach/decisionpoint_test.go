package coach

import (
	"fmt"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

func tipCfg(mut func(*config.Config)) config.Config {
	c := config.Default()
	if mut != nil {
		mut(&c)
	}
	return c
}

func TestTipGateHeld(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	one := 1
	cases := []struct {
		name   string
		cfg    config.Config
		ledger []ft.Entry
		object string
		want   string
	}{
		{"defaults speak", tipCfg(nil), nil, "a.go", ""},
		{"inform off is silent", tipCfg(func(c *config.Config) { c.Coach.Powers = map[string]string{"inform": "off"} }), nil, "a.go", "inform off"},
		{"quiet verbosity is silent", tipCfg(func(c *config.Config) { c.Coach.Verbosity = "quiet" }), nil, "a.go", "quiet"},
		{"the configured hourly budget holds", tipCfg(func(c *config.Config) { c.Coach.Tips.PerHour = &one }),
			[]ft.Entry{{Type: ft.EntryOffer, ID: "x", At: now.Add(-time.Minute), Session: "s", Kind: "search_loop", Point: PointToolCall, Object: "Grep", Channel: ft.ChannelTip}},
			"a.go", ft.HeldHourly},
		{"the same tip in the cool-down holds", tipCfg(nil),
			[]ft.Entry{{Type: ft.EntryOffer, ID: "x", At: now.Add(-time.Minute), Session: "s", Kind: "reread_churn", Point: PointToolCall, Object: "a.go", Channel: ft.ChannelTip}},
			"a.go", ft.HeldCooldown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := NewTipGate(c.cfg, &memLedger{entries: c.ledger}, now)
			if got := g.Held("s", config.PowerInform, "reread_churn", c.object); got != c.want {
				t.Fatalf("Held = %q; want %q", got, c.want)
			}
		})
	}
}

func TestTipGateDeliverRecordsAndThrottlesWithinOneCall(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	l := &memLedger{}
	g := NewTipGate(tipCfg(nil), l, now)
	tips := []DecisionTip{
		{Kind: "window_fit", Point: PointPrepareWork, Object: "Claude 5h window", Message: "m1"},
		{Kind: "window_fit", Point: PointPrepareWork, Object: "Claude 5h window", Message: "m1 again"},
		{Kind: "model_fit", Point: PointPrepareWork, Object: "claude-haiku-4-5", Message: "m2"},
	}
	got := g.Deliver("", "workflow:1", tips)
	if len(got) != 2 || got[0].ID == "" || got[1].Kind != "model_fit" {
		t.Fatalf("Deliver = %+v; want the window and model tips, once each", got)
	}
	if len(l.entries) != 2 {
		t.Fatalf("ledger = %+v", l.entries)
	}
	e := l.entries[0]
	if e.Via != ft.ViaAgent || e.Point != PointPrepareWork || e.Object != "Claude 5h window" || e.Ref != "workflow:1" || e.Channel != ft.ChannelTip {
		t.Fatalf("offer = %+v", e)
	}
}

func TestRecordAgentOutcomeIsSeenEvidence(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	l := &memLedger{}
	for i := range ft.RetireMinSeen {
		id := fmt.Sprint(i)
		l.entries = append(l.entries, ft.Entry{Type: ft.EntryOffer, ID: id, At: now.Add(-time.Hour), Session: "s",
			Power: config.PowerInform, Channel: ft.ChannelTip, Kind: "subagent_model", Via: ft.ViaAgent, Point: PointSubagent, Object: "Explore"})
		RecordAgentOutcome(l, now, id, "subagent_model", false, "launched Explore on opus again", "")
	}
	r := Status(config.Default(), l, nil, now)
	if len(r.Retired) != 1 || r.Retired[0].Kind != "subagent_model" || r.Retired[0].Ignored != ft.RetireMinSeen {
		t.Fatalf("Retired = %+v", r.Retired)
	}
	if !Quieter(l, now)(config.PowerInform, "subagent_model") {
		t.Fatal("a retired kind is held back")
	}
	ended, err := ReinstateKind(l, now.Add(time.Minute), "subagent_model")
	if err != nil || len(ended) != 1 {
		t.Fatalf("ReinstateKind = %+v, %v", ended, err)
	}
	if Quieter(l, now.Add(2*time.Minute))(config.PowerInform, "subagent_model") {
		t.Fatal("a reinstated kind speaks again")
	}
	if ended, _ := ReinstateKind(l, now.Add(3*time.Minute), "subagent_model"); len(ended) != 0 {
		t.Fatalf("reinstating twice ends nothing more, got %+v", ended)
	}
}
