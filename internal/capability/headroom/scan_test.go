package headroom_test

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// scanFixture is a month of every kind of event plan headroom reads: plan
// turns of two providers, the Claude meter's and Codex's window readings
// (Codex's for two limits), Copilot buckets polled at one moment, a Cursor
// meter, a per-token spend reading, a subscription account's windows and
// metered gateway turns.
func scanFixture(now time.Time) []*eventschema.Envelope {
	var out []*eventschema.Envelope
	add := func(id string, at time.Time, source string, provider eventschema.Provider, cost eventschema.CostSource, tokens int64, attrs map[string]string) {
		out = append(out, &eventschema.Envelope{
			ID: id, SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
			Timestamp: at, Source: source, Attributes: attrs,
			Payload: &eventschema.PromptEvent{
				Provider: provider, RequestModel: "m", InputTokens: tokens, OutputTokens: tokens / 10,
				TotalTokens: tokens + tokens/10, CostSource: cost, SessionID: "s" + strconv.Itoa(len(out)%7),
			},
		})
	}
	for i := 0; i < 600; i++ {
		at := now.Add(-time.Duration(600-i)*70*time.Minute + 69*time.Minute)
		add(fmt.Sprintf("cc%04d", i), at, "claude-code-jsonl", eventschema.ProviderAnthropic, eventschema.CostSourcePlanIncluded,
			int64(1000+i), map[string]string{"granularity": "assistant_turn", "starts_user_message": strconv.FormatBool(i%3 == 0)})
		limit := "codex"
		if i%5 == 0 {
			limit = "codex_other"
		}
		add(fmt.Sprintf("cx%04d", i), at.Add(time.Second), "codex-jsonl", eventschema.ProviderOpenAI, eventschema.CostSourcePlanIncluded,
			int64(2000+i), map[string]string{
				"limit_id": limit, "plan_type": "pro",
				"primary_used_pct": strconv.Itoa(i % 90), "primary_window_min": "300",
				"primary_resets_at":  strconv.FormatInt(now.Add(time.Duration(i%5)*time.Hour).Unix(), 10),
				"secondary_used_pct": strconv.Itoa(i % 70), "secondary_window_min": "10080",
				"secondary_resets_at": strconv.FormatInt(now.Add(72*time.Hour).Unix(), 10),
			})
		if i%10 == 9 {
			add(fmt.Sprintf("me%04d", i), at.Add(2*time.Second), "claude-usage-meter", eventschema.ProviderAnthropic, eventschema.CostSourcePlanIncluded, 0,
				map[string]string{
					"five_hour_used_pct": strconv.Itoa(i % 100), "five_hour_reset_at": now.Add(2 * time.Hour).Format(time.RFC3339),
					"seven_day_used_pct": strconv.Itoa(i % 60), "seven_day_reset_at": now.Add(90 * time.Hour).Format(time.RFC3339),
					"granularity": "quota_snapshot",
				})
		}
		if i%7 == 0 {
			add(fmt.Sprintf("or%04d", i), at.Add(3*time.Second), "proxy", "openrouter", eventschema.CostSourceMetered, int64(300+i), nil)
		}
	}
	for b, bucket := range []map[string]string{
		{"percent_remaining": "40", "quota_reset_date": now.AddDate(0, 0, 9).Format("2006-01-02"), "entitlement": "300"},
		{"percent_remaining": "100", "unlimited": "true", "entitlement": "0"},
		{"percent_remaining": "75", "quota_reset_date": now.AddDate(0, 0, 9).Format("2006-01-02"), "entitlement": "50", "seat": "metered"},
	} {
		for p, at := range []time.Time{now.Add(-50 * time.Hour), now.Add(-20 * time.Minute)} {
			add(fmt.Sprintf("gh%d%d", p, b), at, "github-copilot", eventschema.ProviderGitHub, eventschema.CostSourcePlanIncluded, 0, bucket)
		}
	}
	add("cu1", now.Add(-10*time.Minute), "cursor", eventschema.ProviderCursor, eventschema.CostSourcePlanIncluded, 0,
		map[string]string{"used_pct": "33", "billing_cycle_end": now.AddDate(0, 0, 4).Format(time.RFC3339)})
	add("pt1", now.Add(-12*time.Minute), "fireworks", "fireworks", eventschema.CostSourceMetered, 0,
		map[string]string{"billing": "per_token", "extra_usage_limit": "100", "extra_usage_used": "12"})
	add("sub1", now.Add(-time.Hour), "kimi", "kimi", eventschema.CostSourcePlanIncluded, 0,
		map[string]string{"billing": "subscription", "window_0_used_pct": "20", "window_0_name": "5h", "window_0_duration_min": "300",
			"window_1_used_pct": "91", "window_1_name": "week", "window_1_duration_min": "10080"})
	// In the store's order: time, then id.
	slices.SortFunc(out, func(a, b *eventschema.Envelope) int {
		if c := a.Timestamp.Compare(b.Timestamp); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// Plan headroom computed from the event cache, whose events its questions
// scan one at a time, is the headroom computed from envelopes read in
// full; and none of those questions builds the month of envelopes.
func TestHeadroomFromScannedEventsMatchesReadEvents(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "e.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.AppendBatch(ctx, scanFixture(now)); err != nil {
		t.Fatal(err)
	}
	cfg := cfgWith(map[string]string{"anthropic": "claude-ultra", "openai": "gpt-pro-5x", "github": "copilot-pro-plus", "cursor": "cursor-pro"})
	want, err := headroom.Compute(ctx, headroom.Deps{Config: cfg, Reader: storeReader{store}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(want.Reports) < 5 {
		t.Fatalf("the fixture yields %d reports; it should exercise every provider", len(want.Reports))
	}
	cache := &readCounter{EventCache: sqlite.NewEventCache(store, 0, 0)}
	got, err := headroom.Compute(ctx, headroom.Deps{Config: cfg, Reader: cache}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned headroom differs:\ngot  %+v\nwant %+v", got, want)
	}
	if cache.reads != 0 {
		t.Errorf("plan headroom read the events in full %d times; its questions should scan", cache.reads)
	}
}

// readCounter counts the cache's full reads.
type readCounter struct {
	*sqlite.EventCache
	reads int
}

func (r *readCounter) ReadEvents(ctx context.Context, t eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	r.reads++
	return r.EventCache.ReadEvents(ctx, t, since)
}

var _ plans.EventScanner = (*readCounter)(nil)

// poisonScanner scans its events through one envelope it reuses and
// spoils after every call, so a question that keeps what a scan handed it
// answers wrong.
type poisonScanner struct {
	events []*eventschema.Envelope
}

func (p poisonScanner) ReadEvents(context.Context, eventschema.EventType, time.Time) ([]*eventschema.Envelope, error) {
	return nil, fmt.Errorf("read in full; the question should scan")
}

func (p poisonScanner) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return map[string]int64{}, nil
}

func (p poisonScanner) ScanEvents(_ context.Context, _ eventschema.EventType, since time.Time, newestFirst bool, fn func(*eventschema.Envelope) bool) error {
	var env eventschema.Envelope
	var prompt eventschema.PromptEvent
	spoil := func() {
		prompt = eventschema.PromptEvent{Provider: "spoiled", InputTokens: -1, TotalTokens: -1, CostUSD: -1}
		env = eventschema.Envelope{ID: "spoiled", Source: "spoiled", Timestamp: time.Unix(1, 0), Payload: &prompt,
			Attributes: map[string]string{"spoiled": "true"}}
	}
	for i := range p.events {
		e := p.events[i]
		if newestFirst {
			e = p.events[len(p.events)-1-i]
		}
		if e.Timestamp.Before(since) {
			continue
		}
		env = *e
		prompt = *e.Payload.(*eventschema.PromptEvent)
		env.Payload = &prompt
		more := fn(&env)
		spoil()
		if !more {
			break
		}
	}
	return nil
}

// A question keeps nothing a scan hands it: through a scanner that reuses
// and spoils its envelope, headroom is what envelopes read in full give.
func TestHeadroomKeepsNoScannedEnvelope(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	events := scanFixture(now)
	cfg := cfgWith(map[string]string{"anthropic": "claude-ultra", "openai": "gpt-pro-5x", "github": "copilot-pro-plus", "cursor": "cursor-pro"})
	want, err := headroom.Compute(ctx, headroom.Deps{Config: cfg, Reader: &sliceReader{events}}, now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := headroom.Compute(ctx, headroom.Deps{Config: cfg, Reader: poisonScanner{events}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("headroom from a spoiling scanner differs:\ngot  %+v\nwant %+v", got, want)
	}
}

// sliceReader reads its events in full, in time order.
type sliceReader struct{ events []*eventschema.Envelope }

func (r *sliceReader) ReadEvents(_ context.Context, _ eventschema.EventType, since time.Time) ([]*eventschema.Envelope, error) {
	var out []*eventschema.Envelope
	for _, e := range r.events {
		if !e.Timestamp.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (r *sliceReader) CountBySource(context.Context, time.Time, time.Time) (map[string]int64, error) {
	return map[string]int64{}, nil
}
