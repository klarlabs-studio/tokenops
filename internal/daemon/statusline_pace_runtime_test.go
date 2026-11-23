package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/statuslinepace"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// paceEvents is an event store holding one quota reading.
type paceEvents struct{ events []*eventschema.Envelope }

func (p paceEvents) ReadEvents(context.Context, eventschema.EventType, time.Time) ([]*eventschema.Envelope, error) {
	return p.events, nil
}

func (p paceEvents) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return map[string]int64{}, nil
}

// The status line's forecast is written by the daemon from the plan
// headroom, with no glance requested: the file exists and describes the
// window on the line straight after start and at each later write.
func TestStatuslinePaceKeeperWritesWithoutAGlanceRequest(t *testing.T) {
	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour).Truncate(time.Second)
	reading := &eventschema.Envelope{Source: "claude-code-statusline", Timestamp: now.Add(-time.Minute),
		Type: eventschema.EventTypePrompt, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
		Attributes: map[string]string{
			"five_hour_used_pct": "95.00", "five_hour_reset_at": reset.Format(time.RFC3339),
			"granularity": "quota_snapshot",
		}}
	cfg := &config.Config{Plans: map[string]string{"anthropic": "claude-ultra"}}
	glance := func() headroom.Deps {
		return headroom.Deps{Config: cfg, Reader: paceEvents{[]*eventschema.Envelope{reading}}}
	}
	path := filepath.Join(t.TempDir(), statuslinepace.FileName)
	var warned error
	k := statuslinePaceKeeper(glance, path, func() time.Time { return now }, func(err error) { warned = err })

	if err := k.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	f, ok := statuslinepace.Read(path)
	if !ok {
		t.Fatal("no pace file written")
	}
	if !f.ComputedAt.Equal(now) {
		t.Errorf("computed at %v, want %v", f.ComputedAt, now)
	}
	out, rate, ok := f.RunsOut("anthropic", "5h", reset, now)
	if !ok || !out.Before(reset) || rate == "" {
		t.Fatalf("forecast %v %q %v in %+v", out, rate, ok, f.Windows)
	}
	if warned != nil {
		t.Errorf("warned %v", warned)
	}
}
