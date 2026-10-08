package pisessions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func readAll(t *testing.T, path string) []Turn {
	t.Helper()
	var out []Turn
	if err := ReadFile(path, func(tr Turn) error { out = append(out, tr); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadFileMapsAssistantTurns(t *testing.T) {
	turns := readAll(t, filepath.Join("testdata", "pi", "--corpus--", "2026-09-30T12-00-00-000Z_pi-proof-session.jsonl"))
	// The user's message and a backend TokenOps does not know are skipped.
	if len(turns) != 3 {
		t.Fatalf("got %d turns: %+v", len(turns), turns)
	}
	first := turns[0]
	if first.ID != "pi-initial" || first.SessionID != "pi-proof-session" || first.Provider != eventschema.ProviderAnthropic ||
		first.Model != "claude-sonnet-4-6" || first.Input != 40000 || first.Project != "--corpus--" ||
		!first.Timestamp.Equal(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("first %+v", first)
	}
	if c := turns[1]; c.Provider != eventschema.ProviderOpenAI || c.Backend != "openai-codex" || c.Output != 5 || c.ID == "" {
		t.Errorf("codex %+v", c)
	}
	// A turn with no provider or model of its own takes the model change
	// before it, and its own millisecond timestamp.
	if c := turns[2]; c.Provider != eventschema.ProviderOpenAI || c.Model != "gpt-5.3-codex" || c.CacheRead != 2 ||
		!c.Timestamp.Equal(time.UnixMilli(1775131200000).UTC()) {
		t.Errorf("inherited %+v", c)
	}
}

// OMP spells the one-hour cache write cttl.ephemeral1h.
func TestReadFileOMP(t *testing.T) {
	turns := readAll(t, filepath.Join("testdata", "omp", "abs-pi-family-project-0bff77cc", "2026-08-03T12-00-00-000Z_omp-fixture.jsonl"))
	if len(turns) != 1 {
		t.Fatalf("got %+v", turns)
	}
	if tr := turns[0]; tr.SessionID != "omp-fixture" || tr.CacheWrite != 100 || tr.CacheWrite1h != 40 || tr.Input != 80 || tr.Output != 20 {
		t.Errorf("omp %+v", tr)
	}
}

func TestReadUsageRejectsMalformedCounters(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	lines := `{"type":"message","id":"a","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"input":-1,"output":3}}}
{"type":"message","id":"b","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"input":true,"output":3}}}
{"type":"message","id":"c","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"cacheWrite":10,"cacheWrite1h":11}}}
{"type":"message","id":"d","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"inputTokens":"7","output_tokens":3.4}}}
{"type":"message","id":"e","message":{"role":"assistant","provider":"anthropic","model":"m","usage":{"input":0,"output":0}}}
{"type":"message","id":"f","message":{"role":"assistant","prov`
	if err := os.WriteFile(p, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	turns := readAll(t, p)
	if len(turns) != 1 || turns[0].ID != "d" || turns[0].Input != 7 || turns[0].Output != 3 {
		t.Errorf("got %+v", turns)
	}
}

type captureBus struct{ got []*eventschema.Envelope }

func (b *captureBus) Publish(env *eventschema.Envelope) { b.got = append(b.got, env) }
func (b *captureBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.got = append(b.got, env)
	return nil
}
func (b *captureBus) DroppedCount() int64       { return 0 }
func (b *captureBus) PublishedCount() int64     { return int64(len(b.got)) }
func (b *captureBus) Close(time.Duration) error { return nil }

var _ events.Bus = (*captureBus)(nil)

// Every turn is published once, across roots and scans, under the
// provider that served it.
func TestPollerPublishesEachTurnOnce(t *testing.T) {
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{CostSource: func(p eventschema.Provider) eventschema.CostSource {
		if p == eventschema.ProviderAnthropic {
			return eventschema.CostSourcePlanIncluded
		}
		return ""
	}})
	roots := []string{filepath.Join("testdata", "pi"), filepath.Join("testdata", "omp")}
	p.Scan(context.Background(), roots)
	if len(bus.got) != 4 {
		t.Fatalf("published %d", len(bus.got))
	}
	env := bus.got[0]
	pe := env.Payload.(*eventschema.PromptEvent)
	if env.Source != SourceTag || pe.Provider != eventschema.ProviderAnthropic || pe.InputTokens != 40000 ||
		pe.CostSource != eventschema.CostSourcePlanIncluded || env.Attributes["harness"] != "pi" {
		t.Errorf("first envelope %+v %+v", env, pe)
	}
	omp := bus.got[3].Payload.(*eventschema.PromptEvent)
	if omp.InputTokens != 184 || omp.CachedInputTokens != 4 || omp.CacheWriteInputTokens != 100 || omp.CacheWrite1hInputTokens != 40 || omp.TotalTokens != 204 {
		t.Errorf("omp envelope %+v", omp)
	}
	p.Scan(context.Background(), roots)
	p2 := NewPoller(bus, PollerOptions{})
	p2.seen = p.seen
	p2.Scan(context.Background(), roots)
	if len(bus.got) != 4 {
		t.Errorf("published again: %d", len(bus.got))
	}
}
