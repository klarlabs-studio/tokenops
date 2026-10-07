package mcp

import (
	"flag"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// updateGolden rewrites the characterization goldens instead of comparing.
var updateGolden = flag.Bool("update-golden", false, "rewrite testdata goldens")

// timestamps matches the RFC3339 instants a payload carries, which move
// with the clock for windows anchored on now.
var timestamps = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)

// assertGolden compares got with testdata/<name>, timestamps masked, or
// rewrites it under -update-golden.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	got = timestamps.ReplaceAllString(got, "<T>")
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update-golden)", path, err)
	}
	if string(want) != got {
		t.Errorf("%s changed:\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

// metered is a prompt event priced at ingest, the way the proxy records it.
func metered(id string, at time.Time, model string, tokens int64, cost float64) *eventschema.Envelope {
	env := promptEnv(id, at, model, tokens, eventschema.CostSourceMetered)
	env.Payload.(*eventschema.PromptEvent).CostUSD = cost
	return env
}

// TestSpendToolsCharacterization pins the spend tools' results so moving
// their use cases into internal/capability cannot change what an agent
// reads. Windows anchored on now are seeded on whole hours and days, so
// each event lands in the same bucket on every run.
func TestSpendToolsCharacterization(t *testing.T) {
	now := time.Now().UTC()
	hour := now.Truncate(time.Hour)
	day := now.Truncate(24 * time.Hour)
	fixed := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)

	envs := []*eventschema.Envelope{
		// Fixed window for summary and top consumers.
		metered("f1", fixed, "claude-haiku-4-5", 400_000, 1.25),
		metered("f2", fixed.Add(time.Hour), "claude-sonnet-4-6", 300_000, 2.50),
		promptEnv("f3", fixed.Add(2*time.Hour), "claude-opus-5", 900_000, eventschema.CostSourcePlanIncluded),
		promptEnv("f4", fixed.Add(3*time.Hour), "claude-unpriced-model", 1_000, eventschema.CostSourceMetered),
		// The last day, hour by hour, for the burn rate.
		metered("b1", hour.Add(-3*time.Hour+time.Minute), "claude-haiku-4-5", 100_000, 0.40),
		promptEnv("b2", hour.Add(-2*time.Hour+time.Minute), "claude-opus-5", 250_000, eventschema.CostSourcePlanIncluded),
	}
	srv := analyticsServer(t, envs...)

	// The last days, day by day, for the forecast. A store of its own: the
	// hourly events above fall on yesterday or today depending on the hour
	// the test runs, which would move the daily history.
	var daily []*eventschema.Envelope
	for d := 2; d <= 6; d++ {
		daily = append(daily, metered("d"+strconv.Itoa(d), day.AddDate(0, 0, -d).Add(time.Hour),
			"claude-haiku-4-5", int64(50_000*d), 0.3*float64(d)))
	}
	forecastSrv := analyticsServer(t, daily...)

	window := map[string]any{"since": "2026-01-01T00:00:00Z", "until": "2026-01-03T00:00:00Z"}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range window {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	cases := []struct {
		golden string
		srv    *Server
		tool   string
		args   any
	}{
		{"spend/summary.json", srv, "tokenops_spend_summary", window},
		{"spend/top_model.json", srv, "tokenops_top_consumers", with(nil)},
		{"spend/top_provider.json", srv, "tokenops_top_consumers", with(map[string]any{"by": "provider", "top": 1})},
		{"spend/burn_rate.md", srv, "tokenops_burn_rate", nil},
		{"spend/burn_rate_6h.md", srv, "tokenops_burn_rate", map[string]any{"hours": 6}},
		{"spend/forecast.json", forecastSrv, "tokenops_forecast", map[string]any{"horizon_days": 3}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			out := execTool(t, tc.srv, tc.tool, tc.args)
			if strings.HasSuffix(tc.golden, "forecast.json") {
				out = roundFloats(out)
			}
			assertGolden(t, tc.golden, out)
		})
	}
}

// TestForecastCharacterizationShortHistory pins the answer for a store too
// young to project.
func TestForecastCharacterizationShortHistory(t *testing.T) {
	srv := analyticsServer(t, metered("only", time.Now().UTC().Add(-time.Hour), "claude-haiku-4-5", 1_000, 0.01))
	assertGolden(t, "spend/forecast_short.json", execTool(t, srv, "tokenops_forecast", nil))
}

// floatLiterals matches the fractional and exponent numbers in a JSON payload.
var floatLiterals = regexp.MustCompile(`-?\d+\.\d+(?:[eE][-+]?\d+)?|-?\d+[eE][-+]?\d+`)

// roundFloats rounds every fractional number in s to nine decimal places.
//
// The forecaster's arithmetic is not bit-identical across architectures:
// Go fuses a*b+c into one FMA instruction on arm64 and not on amd64, so a
// projection computed on a laptop and on CI differs in the last bits
// (0.30000000000000004 against 0.30000000000000016, 1e-16 against 2e-16).
// The goldens pin the answer, not the rounding of the machine that wrote
// them.
func roundFloats(s string) string {
	return floatLiterals.ReplaceAllStringFunc(s, func(lit string) string {
		v, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			return lit
		}
		r := math.Round(v*1e9) / 1e9
		if r == 0 {
			r = 0 // no "-0"
		}
		return strconv.FormatFloat(r, 'f', -1, 64)
	})
}
