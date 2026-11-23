package plans

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// digestEvents is three weeks of a fictional machine's prompt events:
// Claude meter readings and Codex turns carrying their windows (some of a
// limit other than the plan's, many repeating the reading before), and
// turns on and off the plan, of either provider, some not per request.
func digestEvents(rng *rand.Rand, end time.Time) []*eventschema.Envelope {
	var envs []*eventschema.Envelope
	costs := []eventschema.CostSource{eventschema.CostSourcePlanIncluded, eventschema.CostSourceMetered, eventschema.CostSourceTrial, ""}
	for at := end.Add(-21 * 24 * time.Hour); at.Before(end); at = at.Add(time.Duration(5+rng.Intn(40)) * time.Minute) {
		week := at.Truncate(7 * 24 * time.Hour).Add(7 * 24 * time.Hour)
		five := at.Truncate(5 * time.Hour).Add(5 * time.Hour)
		switch rng.Intn(4) {
		case 0:
			envs = append(envs, &eventschema.Envelope{Source: "claude-usage-meter", Timestamp: at, Attributes: map[string]string{
				"granularity":    "quota_snapshot",
				"five_hour_kind": "session", "five_hour_used_pct": strconv.Itoa(rng.Intn(101)), "five_hour_reset_at": five.Format(time.RFC3339),
				"seven_day_kind": "weekly_all", "seven_day_used_pct": strconv.Itoa(rng.Intn(101)), "seven_day_reset_at": week.Format(time.RFC3339),
			}, Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}})
		default:
			attrs := map[string]string{"granularity": "assistant_turn",
				"primary_used_pct": strconv.Itoa(rng.Intn(3) * 10), "primary_window_min": "300", "primary_resets_at": strconv.FormatInt(five.Unix(), 10),
				"secondary_used_pct": "40", "secondary_window_min": "10080", "secondary_resets_at": strconv.FormatInt(week.Unix(), 10)}
			if rng.Intn(10) == 0 {
				attrs["limit_id"] = "premium"
			}
			if rng.Intn(10) == 0 {
				attrs["granularity"] = "quota_snapshot"
			}
			provider := eventschema.ProviderOpenAI
			if rng.Intn(5) == 0 {
				provider = eventschema.ProviderAnthropic
			}
			envs = append(envs, &eventschema.Envelope{Source: "codex-jsonl", Timestamp: at, Attributes: attrs,
				Payload: &eventschema.PromptEvent{Provider: provider, RequestModel: "gpt-5", CostSource: costs[rng.Intn(len(costs))],
					InputTokens: int64(rng.Intn(1000)), OutputTokens: int64(rng.Intn(200)), TotalTokens: int64(rng.Intn(2) * rng.Intn(1500))}})
		}
	}
	return envs
}

// digestPrice prices a turn by its tokens, and has no rate for one in
// ten.
func digestPrice(p *eventschema.PromptEvent, _ time.Time) (float64, error) {
	if p.InputTokens%10 == 0 {
		return 0, errors.New("no rate")
	}
	return float64(p.InputTokens+p.OutputTokens) * 1e-6, nil
}

// A digest answers what the functions it stands for answer over the
// events it was built from: plan use on each day, the plan's window
// readings and the plan value.
func TestUseDigestAnswersAsTheEvents(t *testing.T) {
	end := time.Date(2026, 10, 14, 15, 30, 0, 0, time.UTC)
	envs := digestEvents(rand.New(rand.NewSource(7)), end)
	first := end.Truncate(24*time.Hour).AddDate(0, 0, -13)
	d := DigestOf(envs, digestPrice, first)
	var utilized, valued int
	for day := first; day.Before(end); day = day.AddDate(0, 0, 1) {
		until := day.Add(24*time.Hour - time.Nanosecond)
		if until.After(end) {
			until = end
		}
		got, want := d.PlanUseOn(day, until), PlanUseOn(envs, day, until)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: plan use = %+v, want %+v", day.Format(time.DateOnly), got, want)
		}
		for _, u := range want.Providers {
			if u.UtilizationPct != nil {
				utilized++
			}
		}
		for _, p := range []string{"anthropic", "openai"} {
			usd, unpriced := d.ListValue(p, day, until)
			wantUSD, wantUnpriced := ListValue(envs, p, day, until, digestPrice)
			if wantUSD > 0 && wantUnpriced > 0 {
				valued++
			}
			if usd != wantUSD || unpriced != wantUnpriced {
				t.Errorf("%s %s: value = %v (%d unpriced), want %v (%d)", day.Format(time.DateOnly), p, usd, unpriced, wantUSD, wantUnpriced)
			}
		}
	}
	if utilized == 0 || valued == 0 {
		t.Fatalf("the events measure %d days' utilization and value %d, want some of each", utilized, valued)
	}
	for _, p := range []string{"anthropic", "openai"} {
		if got, want := d.QuotaReadings(p, time.Time{}, end), QuotaReadingsFrom(envs, p, time.Time{}, end); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %d readings, want %d", p, len(got), len(want))
		}
	}
	if usd, unpriced := DigestOf(envs, nil, first).ListValue("openai", first, end); usd != 0 || unpriced != 0 {
		t.Errorf("a digest built without a price valued %v (%d unpriced)", usd, unpriced)
	}
}

// scanOnly scans its events and fails a read of them as a slice.
type scanOnly struct {
	t    *testing.T
	envs []*eventschema.Envelope
}

func (r scanOnly) ReadEvents(context.Context, eventschema.EventType, time.Time) ([]*eventschema.Envelope, error) {
	r.t.Error("the events were read as a slice from a reader that scans")
	return nil, nil
}

func (r scanOnly) ScanEvents(_ context.Context, _ eventschema.EventType, since time.Time, _ bool, fn func(*eventschema.Envelope) bool) error {
	var reused eventschema.Envelope
	var prompt eventschema.PromptEvent
	for _, e := range r.envs {
		if e.Timestamp.Before(since) {
			continue
		}
		// One envelope for every event, as the event cache scans.
		reused, prompt = *e, *e.Payload.(*eventschema.PromptEvent)
		reused.Payload = &prompt
		if !fn(&reused) {
			return nil
		}
	}
	return nil
}

// A digest is built by scanning a reader that scans, so the events are
// never held, and keeps nothing of the envelope it was handed.
func TestDigestUseScans(t *testing.T) {
	end := time.Date(2026, 10, 14, 15, 30, 0, 0, time.UTC)
	envs := digestEvents(rand.New(rand.NewSource(8)), end)
	first := end.Truncate(24*time.Hour).AddDate(0, 0, -6)
	since := first.AddDate(0, 0, -7)
	got, err := DigestUse(context.Background(), scanOnly{t, envs}, since, digestPrice, first)
	if err != nil {
		t.Fatal(err)
	}
	var in []*eventschema.Envelope
	for _, e := range envs {
		if !e.Timestamp.Before(since) {
			in = append(in, e)
		}
	}
	if want := DigestOf(in, digestPrice, first); !reflect.DeepEqual(got, want) {
		t.Errorf("the scanned digest differs from the read one")
	}
}
