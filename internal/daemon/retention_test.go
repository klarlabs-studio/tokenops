package daemon

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/retention"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestRetentionPolicies(t *testing.T) {
	pols, err := retentionPolicies(config.RetentionConfig{
		Keep: map[string]string{"prompt": "30d", "workflow": "0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pols) != 1 {
		t.Fatalf("got %d policies; want 1 (zero window skipped)", len(pols))
	}
	if pols[0].EventType != eventschema.EventTypePrompt || pols[0].KeepFor != 30*24*time.Hour {
		t.Fatalf("policy = %+v", pols[0])
	}
}

func TestRetentionPoliciesRejectUnknown(t *testing.T) {
	if _, err := retentionPolicies(config.RetentionConfig{Keep: map[string]string{"nope": "1h"}}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRetentionPoliciesFromSourceWindows(t *testing.T) {
	pols, err := retentionPolicies(config.RetentionConfig{
		Keep: map[string]string{"prompt": "120d"},
		KeepBySource: map[string]string{
			"opencode":          "0",
			"codex-jsonl":       "365d",
			"prompt:read-guard": "7d",
		},
	})
	if err != nil {
		t.Fatalf("retentionPolicies: %v", err)
	}
	if len(pols) != 4 {
		t.Fatalf("policies = %d, want 4 (1 type + 3 source)", len(pols))
	}
	bySource := map[string]retention.Policy{}
	for _, p := range pols {
		bySource[p.Source] = p
	}
	// A forever window must still be emitted: the pruner needs it to
	// exclude the source from the type-wide delete.
	oc, ok := bySource["opencode"]
	if !ok {
		t.Fatal("opencode policy missing; its rows would fall to the type window")
	}
	if oc.KeepFor != 0 {
		t.Errorf("opencode KeepFor = %s, want 0 (keep forever)", oc.KeepFor)
	}
	if got := bySource["codex-jsonl"].KeepFor; got != 365*24*time.Hour {
		t.Errorf("codex-jsonl KeepFor = %s, want 8760h", got)
	}
	if got := bySource["read-guard"].KeepFor; got != 7*24*time.Hour {
		t.Errorf("read-guard KeepFor = %s, want 168h", got)
	}
	if got := bySource["read-guard"].EventType; got != eventschema.EventTypePrompt {
		t.Errorf("read-guard EventType = %q, want prompt", got)
	}
}

func TestRetentionPoliciesRejectUnknownSourceType(t *testing.T) {
	if _, err := retentionPolicies(config.RetentionConfig{
		KeepBySource: map[string]string{"bogus:opencode": "30d"},
	}); err == nil {
		t.Fatal("expected an error for an unknown event type")
	}
}

// The daemon's first prune must not run on top of the startup replay, and
// a StartDelay the daemon never passes delays nothing.
func TestRetentionConfigHoldsTheFirstPass(t *testing.T) {
	c := retentionConfig(config.RetentionConfig{Interval: 6 * time.Hour, Reclaim: true}, nil, nil)
	if c.StartDelay < time.Minute {
		t.Errorf("StartDelay = %s; the first pass would overlap the startup replay", c.StartDelay)
	}
	if c.Interval != 6*time.Hour || !c.Reclaim {
		t.Errorf("config not carried through: interval=%s reclaim=%v", c.Interval, c.Reclaim)
	}
}
