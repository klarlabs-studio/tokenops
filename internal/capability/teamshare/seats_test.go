package teamshare

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/pkg/eventschema"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// seatModel is the model the fictional turns ran on: a name that must not
// cross.
const seatModel = "claude-opus-canary-model"

// seatEvents is a fictional machine on a plan whose week resets three days
// from now, 70% used four days in, beside a 5-hour window at 90%; and a
// plan-covered turn on each of the last three days, plus a turn billed per
// token.
func seatEvents() []*eventschema.Envelope {
	weekReset := now.Add(3 * 24 * time.Hour)
	reading := &eventschema.Envelope{Source: "claude-usage-meter", Timestamp: now.Add(-10 * time.Minute),
		Attributes: map[string]string{
			"granularity":    "quota_snapshot",
			"seven_day_kind": "weekly_all", "seven_day_used_pct": "70.40", "seven_day_reset_at": weekReset.Format(time.RFC3339),
			"five_hour_kind": "session", "five_hour_used_pct": "90.00", "five_hour_reset_at": now.Add(2 * time.Hour).Format(time.RFC3339),
		},
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}}
	turn := func(at time.Time, cs eventschema.CostSource, tokens int64) *eventschema.Envelope {
		return &eventschema.Envelope{Source: "claude-code-jsonl", Timestamp: at,
			Attributes: map[string]string{"granularity": "assistant_turn"},
			Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic, RequestModel: seatModel,
				CostSource: cs, TotalTokens: tokens}}
	}
	return []*eventschema.Envelope{
		reading,
		turn(now.Add(-50*time.Hour), eventschema.CostSourcePlanIncluded, 400),
		turn(now.Add(-26*time.Hour), eventschema.CostSourcePlanIncluded, 600),
		turn(now.Add(-2*time.Hour), eventschema.CostSourcePlanIncluded, 750),
		turn(now.Add(-time.Hour), eventschema.CostSourcePlanIncluded, 10),
		turn(now.Add(-time.Hour), eventschema.CostSourceMetered, 250),
	}
}

// tokenPrice prices a turn at a cent per ten tokens; a turn of 10 tokens
// has no rate.
func tokenPrice(p *eventschema.PromptEvent, _ time.Time) (float64, error) {
	if p.TotalTokens == 10 {
		return 0, errors.New("no rate")
	}
	return float64(p.TotalTokens) / 1000, nil
}

// seatDeps is a machine with Claude bound and a ChatGPT seat it never
// used, which it shares with someone else.
func seatDeps() Deps {
	d := deps()
	cfg := &config.Config{Plans: map[string]string{"anthropic": "claude-max-20x", "openai": "gpt-plus"},
		Team: config.TeamConfig{SharedPlans: []string{"openai"}}}
	history := plans.History{
		{Provider: "anthropic", Plan: "claude-max-5x", From: now.AddDate(0, 0, -40)},
		{Provider: "anthropic", Plan: "claude-max-20x", From: time.Date(2026, 9, 20, 18, 0, 0, 0, time.UTC)},
	}
	d.FinOps = FinOps{
		Plans: func() headroom.Deps {
			return headroom.Deps{Config: cfg, Reader: eventsReader{seatEvents()}, Price: tokenPrice,
				PlanHistory: func() (plans.History, error) { return history, nil }}
		},
		Waste: func(context.Context, TurnReader, time.Time, time.Time) ([]avoidable.Day, error) {
			return wasteDays(), nil
		},
	}
	return d
}

// A binding is sent for every plan bound, used or not, with the date the
// plan history has it in force from and the member's shared flag. A
// binding the catalog does not know is not sent.
func TestPlanBindings(t *testing.T) {
	history := plans.History{
		{Provider: "anthropic", Plan: "claude-max", From: now.AddDate(0, 0, -60)},
		{Provider: "anthropic", Plan: "claude-ultra", From: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)},
		// A price change on the same plan does not restart it; a retired
		// name is the same plan.
		{Provider: "anthropic", Plan: "claude-max-20x", From: time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC), Price: 180},
	}
	bound := map[string]string{"anthropic": "claude-ultra", "openai": "gpt-plus", "cursor": "a plan of my own"}
	got := PlanBindings(bound, history, []string{"openai", "zai"}, now)
	want := []teamwire.PlanBinding{
		{Provider: "anthropic", Plan: "claude-ultra", Since: "2026-09-01"},
		{Provider: "openai", Plan: "gpt-plus", Shared: true},
	}
	if len(got) != len(want) {
		t.Fatalf("bindings = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("binding %d = %+v, want %+v", i, got[i], want[i])
		}
		if err := got[i].Validate(); err != nil {
			t.Errorf("binding %d: %v", i, err)
		}
	}
}

// The forward snapshot is the weekly window alone, in whole points, its
// shortfall in 6-hour steps and its reset as a date: no 5-hour window and
// no instant.
func TestPlanWindows(t *testing.T) {
	bound := map[string]string{"anthropic": "claude-max", "openai": "gpt-plus"}
	got := PlanWindows(context.Background(), eventsReader{seatEvents()}, bound, now)
	// 70.4% four days into the week is 123% by its reset at this pace; it
	// runs out after 4d x 29.6/70.4 = 40.4h, 31.6h before the reset.
	want := teamwire.PlanWindow{Provider: "anthropic", Window: teamwire.WindowWeekly, UsedPct: 70, ProjectedPctAtReset: 123,
		RunsOut: true, ShortHours: 30, ResetsOn: "2026-10-11", Basis: teamwire.BasisAverage}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("windows = %+v, want %+v", got, want)
	}
	if err := got[0].Validate(); err != nil {
		t.Fatal(err)
	}
	// Early in the week, nothing is projected.
	early := now.Add(-3*24*time.Hour - 12*time.Hour)
	envs := []*eventschema.Envelope{{Source: "claude-usage-meter", Timestamp: early.Add(-time.Minute),
		Attributes: map[string]string{"granularity": "quota_snapshot", "seven_day_kind": "weekly_all",
			"seven_day_used_pct": "5", "seven_day_reset_at": early.Add(7*24*time.Hour - 2*time.Hour).Format(time.RFC3339)},
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}}}
	got = PlanWindows(context.Background(), eventsReader{envs}, bound, early)
	if len(got) != 1 || !got[0].TooEarly || got[0].ProjectedPctAtReset != 0 || got[0].RunsOut || got[0].UsedPct != 5 {
		t.Fatalf("early windows = %+v", got)
	}
	// A plan with only a 5-hour window has no row.
	envs = []*eventschema.Envelope{{Source: "claude-usage-meter", Timestamp: now.Add(-time.Minute),
		Attributes: map[string]string{"granularity": "quota_snapshot", "five_hour_kind": "session",
			"five_hour_used_pct": "90", "five_hour_reset_at": now.Add(time.Hour).Format(time.RFC3339)},
		Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}}}
	if got := PlanWindows(context.Background(), eventsReader{envs}, bound, now); len(got) != 0 {
		t.Errorf("a 5-hour window crossed: %+v", got)
	}
}

// Each plan day's value is the figure plans.ListValue gives the replay
// for that day.
func TestPlanValueIsTheDaysListValue(t *testing.T) {
	envs := seatEvents()
	days := CoveredDays(now, 4)
	rows := PlanDays(envs, map[string]string{"anthropic": "claude-max"}, nil, days, now)
	AddPlanValue(rows, envs, tokenPrice, now)
	if len(rows) != 3 {
		t.Fatalf("plan days = %+v", rows)
	}
	for _, r := range rows {
		start, _ := time.Parse(teamwire.DayLayout, r.Day)
		end := dayEnd(start, now)
		usd, unpriced := plans.ListValue(envs, r.Provider, start, end, tokenPrice)
		if r.PlanValueUSD == nil || *r.PlanValueUSD != usd || r.PlanUnpricedTurns != int64(unpriced) {
			t.Errorf("%s: value %v, unpriced %d; ListValue gives %v, %d", r.Day, r.PlanValueUSD, r.PlanUnpricedTurns, usd, unpriced)
		}
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", r.Day, err)
		}
	}
	if last := rows[2]; *last.PlanValueUSD != 0.75 || last.PlanUnpricedTurns != 1 {
		t.Errorf("today = %+v", last)
	}
	// Without a rate card nothing is priced.
	rows = PlanDays(envs, map[string]string{"anthropic": "claude-max"}, nil, days, now)
	AddPlanValue(rows, envs, nil, now)
	for _, r := range rows {
		if r.PlanValueUSD != nil || r.PlanUnpricedTurns != 0 {
			t.Errorf("%s priced with no rate card: %+v", r.Day, r)
		}
	}
}

// A schema 6 upload carries the seat value: an idle seat's binding, the
// days' value, the advice's options and the weekly snapshot. Built from a
// machine full of prompts, paths and model names, it carries none of
// them, and no instant.
func TestSchema6UploadCarriesSeatValue(t *testing.T) {
	u, notes := Build(context.Background(), seatDeps(), Options{Days: 14, RepoNames: RepoRemote, Schema: teamwire.SchemaSeats}, now)
	// The fixture's turns name no provider, so its spend rows are dropped;
	// nothing of the plans may be.
	if len(notes.Unread) > 0 || strings.Contains(strings.Join(notes.Dropped, " "), "plan") {
		t.Fatalf("notes: %v", notes.All())
	}
	if u.Schema != teamwire.SchemaSeats {
		t.Fatalf("schema = %d", u.Schema)
	}
	if err := u.Validate(); err != nil {
		t.Fatalf("schema 6 upload does not validate: %v", err)
	}
	idle := teamwire.PlanBinding{Provider: "openai", Plan: "gpt-plus", Shared: true}
	if len(u.PlanBindings) != 2 || u.PlanBindings[1] != idle || u.PlanBindings[0].Since != "2026-09-20" {
		t.Errorf("bindings = %+v", u.PlanBindings)
	}
	priced := 0
	for _, d := range u.PlanDays {
		if d.PlanValueUSD != nil {
			priced++
		}
	}
	if priced == 0 || len(u.PlanWindows) != 1 {
		t.Errorf("plan days %+v, windows %+v", u.PlanDays, u.PlanWindows)
	}
	raw, _ := json.Marshal(u)
	for _, c := range append(append([]string{}, canaries...), seatModel, "5h", "five_hour") {
		if strings.Contains(string(raw), c) {
			t.Errorf("upload carries %q:\n%s", c, raw)
		}
	}
	// The only instant is when it was computed.
	instants := regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}`).FindAllString(string(raw), -1)
	if len(instants) != 1 {
		t.Errorf("instants in the upload: %v", instants)
	}

	// Asked for schema 5, the same machine sends none of it.
	u5, _ := Build(context.Background(), seatDeps(), Options{Days: 14, RepoNames: RepoRemote, Schema: teamwire.SchemaPrevented}, now)
	if u5.Schema != teamwire.SchemaPrevented {
		t.Fatalf("schema 5 build = schema %d", u5.Schema)
	}
	if err := u5.Validate(); err != nil {
		t.Errorf("schema 5 upload does not validate: %v", err)
	}
	raw, _ = json.Marshal(u5)
	for _, key := range []string{"plan_value_usd", "list_value_usd", "options", "plan_bindings", "plan_windows"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("schema 5 upload carries %s", key)
		}
	}
}
