package proxy

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/statuslinepace"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Each computed glance leaves its forecast for the status line, which
// reads that file rather than the daemon.
func TestGlanceWritesTheStatuslinePace(t *testing.T) {
	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour).Truncate(time.Second)
	reading := &eventschema.Envelope{Source: "claude-code-statusline", Timestamp: now.Add(-time.Minute),
		Type: eventschema.EventTypePrompt, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic},
		Attributes: map[string]string{
			"five_hour_used_pct": "95.00", "five_hour_reset_at": reset.Format(time.RFC3339),
			"granularity": "quota_snapshot",
		}}
	cfg := &config.Config{Plans: map[string]string{"anthropic": "claude-ultra"}}
	deps := func() headroom.Deps {
		return headroom.Deps{Config: cfg, Reader: planEvents{[]*eventschema.Envelope{reading}}}
	}
	path := filepath.Join(t.TempDir(), statuslinepace.FileName)
	s := New("127.0.0.1:0", WithPlans(deps), WithStatuslinePace(path, "9.9.9"))
	var g headroom.GlancePayload
	if code := getAPI(t, s, "/api/glance", &g); code != http.StatusOK {
		t.Fatalf("GET /api/glance = %d", code)
	}
	f, ok := statuslinepace.Read(path)
	if !ok {
		t.Fatal("no pace file written")
	}
	if f.Version != "9.9.9" || !f.ComputedAt.Equal(g.ComputedAt) {
		t.Errorf("header %+v, glance computed at %v", f, g.ComputedAt)
	}
	out, rate, ok := f.RunsOut("anthropic", "5h", reset, now)
	if !ok || !out.Before(reset) || rate == "" {
		t.Fatalf("forecast %v %q %v in %+v", out, rate, ok, f.Windows)
	}
}
