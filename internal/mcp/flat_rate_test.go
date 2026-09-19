package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// promptEnv builds one prompt event for the flat-rate fixtures.
func promptEnv(id string, at time.Time, model string, tokens int64, src eventschema.CostSource) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID: id, SchemaVersion: eventschema.SchemaVersion,
		Type: eventschema.EventTypePrompt, Timestamp: at, Source: "claude-code-jsonl",
		Payload: &eventschema.PromptEvent{
			Provider: eventschema.ProviderAnthropic, RequestModel: model,
			InputTokens: tokens * 9 / 10, OutputTokens: tokens / 10, TotalTokens: tokens,
			CostSource: src,
		},
	}
}

// analyticsServer registers the spend tools over a temp store seeded with
// envs. Nothing here reaches the operator's real store.
func analyticsServer(t *testing.T, envs ...*eventschema.Envelope) *Server {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "a.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if len(envs) > 0 {
		if err := store.AppendBatch(context.Background(), envs); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	eng := spend.NewEngine(spend.DefaultTable())
	srv := NewServer("tokenops", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := RegisterTools(srv, Deps{Store: store, Spend: eng, Aggregator: analytics.New(store, eng)}); err != nil {
		t.Fatal(err)
	}
	return srv
}

// flatRateMix is a Claude Max operator's store with one small metered call
// in it: two plan-covered models worth far more at list price than the one
// request that actually billed.
func flatRateMix(now time.Time) []*eventschema.Envelope {
	metered := promptEnv("haiku-metered", now.Add(-time.Hour), "claude-haiku-4-5", 1_000, eventschema.CostSourceMetered)
	// Priced at ingest, the way the proxy records metered traffic — the
	// one non-zero cost in the window, which a cost-keyed ranking puts first.
	metered.Payload.(*eventschema.PromptEvent).CostUSD = 0.05
	return []*eventschema.Envelope{
		promptEnv("opus", now.Add(-2*time.Hour), "claude-opus-5", 2_200_000, eventschema.CostSourcePlanIncluded),
		promptEnv("sonnet", now.Add(-90*time.Minute), "claude-sonnet-5", 550_000, eventschema.CostSourcePlanIncluded),
		metered,
	}
}

func decodeTop(t *testing.T, out string) topConsumersResult {
	t.Helper()
	var res topConsumersResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode (%q): %v", out, err)
	}
	return res
}

// consumerKeys is the ranking as one comparable string.
func consumerKeys(res topConsumersResult) string {
	keys := make([]string, 0, len(res.Top))
	for _, e := range res.Top {
		keys = append(keys, e.Key)
	}
	return strings.Join(keys, ",")
}

// On a flat-rate plan every plan-covered row costs $0, so a cost-keyed
// ranking put the one metered haiku call first and left the two models
// that actually carried the work in map order behind it. The CLI ranks on
// the API-equivalent; the agent must be told the same order.
func TestTopConsumersRanksByAPIEquivalentOnAFlatRatePlan(t *testing.T) {
	srv := analyticsServer(t, flatRateMix(time.Now().UTC())...)
	res := decodeTop(t, execTool(t, srv, "tokenops_top_consumers", nil))

	if got, want := consumerKeys(res), "claude-opus-5,claude-sonnet-5,claude-haiku-4-5"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	if res.Top[0].CostUSD != 0 {
		t.Errorf("plan-covered opus must stay $0 real, got %.4f", res.Top[0].CostUSD)
	}
	if res.Top[0].APIEquivalentUSD <= 0 {
		t.Errorf("opus entry carries no api_equivalent_usd: %+v", res.Top[0])
	}
	// "by":"" told the agent nothing about how the answer was grouped.
	if res.By != "model" {
		t.Errorf("By = %q, want the effective grouping echoed", res.By)
	}
}

// Unpriced models tie at zero on both dollar figures; the tokens are what
// is left to rank on, and a map-ordered tie is not an answer. Names are
// chosen so alphabetical order is not also token order.
func TestTopConsumersFallsBackToTokensWhenValueTies(t *testing.T) {
	now := time.Now().UTC()
	srv := analyticsServer(t,
		promptEnv("a", now.Add(-time.Hour), "mystery-a", 10, eventschema.CostSourcePlanIncluded),
		promptEnv("b", now.Add(-time.Hour), "mystery-b", 10_000, eventschema.CostSourcePlanIncluded),
		promptEnv("c", now.Add(-time.Hour), "mystery-c", 1_000, eventschema.CostSourcePlanIncluded),
	)
	for range 10 { // map order varies per call; one lucky pass proves nothing
		res := decodeTop(t, execTool(t, srv, "tokenops_top_consumers", nil))
		if got := consumerKeys(res); got != "mystery-b,mystery-c,mystery-a" {
			t.Fatalf("order = %s, want tokens descending", got)
		}
	}
}

// An unknown grouping fell through to model inside the handler, so a
// typo'd "by" returned a confident answer to a question nobody asked. The
// schema enum refuses it at the MCP boundary; the handler must not depend
// on every caller going through that boundary.
func TestTopConsumersRejectsAnUnknownGrouping(t *testing.T) {
	srv := analyticsServer(t)
	tool, _ := srv.GetTool("tokenops_top_consumers")
	if _, err := tool.Execute(context.Background(), []byte(`{"by":"team"}`)); err == nil {
		t.Fatal("by=team accepted at the tool boundary")
	}

	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "h.db"), sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	eng := spend.NewEngine(spend.DefaultTable())
	d := Deps{Store: store, Spend: eng, Aggregator: analytics.New(store, eng)}
	_, err = topConsumers(context.Background(), d, topConsumersInput{By: "team"})
	if err == nil {
		t.Fatal("by=team accepted by the handler")
	}
	if !strings.Contains(err.Error(), "team") {
		t.Errorf("error should name the rejected value: %v", err)
	}
}

// "Total 0.0000 USD" for 2.2 billion tokens is what a Claude Max operator
// was shown: true of the bill and useless as a burn rate. With cost at zero
// the tokens are the burn, so they lead.
func TestBurnRateLeadsWithTokensWhenCostIsZero(t *testing.T) {
	now := time.Now().UTC()
	srv := analyticsServer(t,
		promptEnv("opus", now.Add(-2*time.Hour), "claude-opus-5", 2_200_000, eventschema.CostSourcePlanIncluded),
		promptEnv("sonnet", now.Add(-90*time.Minute), "claude-sonnet-5", 550_000, eventschema.CostSourcePlanIncluded),
	)
	out := execTool(t, srv, "tokenops_burn_rate", nil)

	summary, payload, ok := strings.Cut(out, "<details>")
	if !ok {
		t.Fatalf("no JSON appendix:\n%s", out)
	}
	if strings.Contains(summary, "| Total | 0.0000") {
		t.Errorf("summary leads with a zero dollar total:\n%s", summary)
	}
	if !strings.Contains(summary, "2750000 tokens") {
		t.Errorf("summary should lead with the token burn:\n%s", summary)
	}
	for _, want := range []string{`"tokens": 2750000`, `"api_equivalent_usd"`} {
		if !strings.Contains(payload, want) {
			t.Errorf("payload missing %s:\n%s", want, payload)
		}
	}
}

// Metered traffic still reads in dollars first; the token figure rides
// along rather than replacing a cost that means something.
func TestBurnRateKeepsDollarsWhenThereIsSpend(t *testing.T) {
	now := time.Now().UTC()
	srv := analyticsServer(t,
		promptEnv("haiku", now.Add(-time.Hour), "claude-haiku-4-5", 1_000_000, eventschema.CostSourceMetered))
	out := execTool(t, srv, "tokenops_burn_rate", nil)
	summary, _, _ := strings.Cut(out, "<details>")
	if !strings.Contains(summary, "| Total |") || strings.Contains(summary, "| Total | 0.0000") {
		t.Errorf("metered burn should lead with a non-zero dollar total:\n%s", summary)
	}
}

// A flat-rate cost history projects a flat zero. Returned bare, that reads
// as a broken forecaster; the note says why and where the signal is.
func TestForecastExplainsAnAllZeroDollarSeries(t *testing.T) {
	now := time.Now().UTC()
	var envs []*eventschema.Envelope
	for d := 1; d <= 4; d++ {
		envs = append(envs, promptEnv("day-"+itoa(d), now.AddDate(0, 0, -d), "claude-opus-5",
			int64(100_000*(5-d)), eventschema.CostSourcePlanIncluded))
	}
	srv := analyticsServer(t, envs...)
	var res forecastResult
	if err := json.Unmarshal([]byte(execTool(t, srv, "tokenops_forecast", nil)), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.ForecastTokens) == 0 || res.ForecastTokens[0].Value <= 0 {
		t.Fatalf("no token signal to point at: %+v", res.ForecastTokens)
	}
	if !strings.Contains(res.Note, "forecast_tokens") {
		t.Errorf("note = %q, want it to point at forecast_tokens", res.Note)
	}
}

// A forecast with real spend in it carries no such note.
func TestForecastWithSpendHasNoZeroNote(t *testing.T) {
	now := time.Now().UTC()
	var envs []*eventschema.Envelope
	for d := 1; d <= 4; d++ {
		env := promptEnv("day-"+itoa(d), now.AddDate(0, 0, -d), "claude-haiku-4-5",
			200_000, eventschema.CostSourceMetered)
		// Priced at ingest, the way the proxy records metered traffic.
		env.Payload.(*eventschema.PromptEvent).CostUSD = 2.5
		envs = append(envs, env)
	}
	srv := analyticsServer(t, envs...)
	var res forecastResult
	if err := json.Unmarshal([]byte(execTool(t, srv, "tokenops_forecast", nil)), &res); err != nil {
		t.Fatal(err)
	}
	if allZeroForecast(res.Forecast) {
		t.Fatalf("fixture produced a zero dollar forecast; it cannot test the metered case: %+v", res.Forecast)
	}
	if res.Note != "" {
		t.Errorf("note = %q on a metered forecast", res.Note)
	}
}
