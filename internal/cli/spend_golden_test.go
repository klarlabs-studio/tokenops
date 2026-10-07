package cli

import (
	"context"
	"flag"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// updateGolden rewrites the characterization goldens instead of comparing.
var updateGolden = flag.Bool("update-golden", false, "rewrite testdata goldens")

// assertGolden compares got with testdata/<name>, or rewrites it under
// -update-golden.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
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

// seedFixedSpendDB writes prompt events at fixed instants in January 2026,
// so every figure the spend command derives from them — window, ranking,
// forecast — is the same on every run. Nothing lands in the last 24 hours,
// so the burn section is deterministic too.
func seedFixedSpendDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	type seed struct {
		day           int
		provider      eventschema.Provider
		model         string
		inTok, outTok int64
		cost          float64
	}
	seeds := []seed{
		{0, eventschema.ProviderOpenAI, "gpt-4o-mini", 1000, 200, 0.50},
		{0, eventschema.ProviderAnthropic, "claude-sonnet-4-6", 800, 300, 1.20},
		{1, eventschema.ProviderOpenAI, "gpt-4o-mini", 1500, 250, 0.75},
		{2, eventschema.ProviderAnthropic, "claude-sonnet-4-6", 600, 200, 0.90},
		{3, eventschema.ProviderOpenAI, "gpt-4o-mini", 2000, 400, 1.00},
		{4, eventschema.ProviderAnthropic, "claude-opus-4-8", 4000, 900, 3.10},
		{5, eventschema.ProviderAnthropic, "claude-sonnet-4-6", 700, 250, 0.80},
	}
	for i, s := range seeds {
		env := &eventschema.Envelope{
			ID:            uuid.NewString(),
			SchemaVersion: eventschema.SchemaVersion,
			Type:          eventschema.EventTypePrompt,
			Timestamp:     base.AddDate(0, 0, s.day).Add(time.Duration(i) * time.Minute),
			Source:        "test",
			Payload: &eventschema.PromptEvent{
				PromptHash:    "sha256:abc",
				Provider:      s.provider,
				RequestModel:  s.model,
				ResponseModel: s.model,
				InputTokens:   s.inTok,
				OutputTokens:  s.outTok,
				TotalTokens:   s.inTok + s.outTok,
				Status:        200,
				CostUSD:       s.cost,
				WorkflowID:    "wf-golden",
				AgentID:       "agent-golden",
			},
		}
		if err := store.Append(ctx, env); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	return path
}

// TestSpendOutputCharacterization pins `tokenops spend` byte for byte, so
// moving its use case into internal/capability cannot change what an
// operator or a script reads.
func TestSpendOutputCharacterization(t *testing.T) {
	path := seedFixedSpendDB(t)
	window := []string{"--since", "2026-01-01T00:00:00Z", "--until", "2026-01-08T00:00:00Z"}
	cases := []struct {
		golden string
		args   []string
	}{
		{"spend/by_model.txt", []string{"--by", "model"}},
		{"spend/by_provider_top2.txt", []string{"--by", "provider", "--top", "2"}},
		{"spend/forecast.txt", []string{"--forecast", "--forecast-days", "3"}},
		{"spend/forecast.json", []string{"--forecast", "--forecast-days", "3", "--json"}},
		{"spend/by_agent.json", []string{"--by", "agent", "--json"}},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			args := append(append([]string{"spend", "--db", path}, window...), tc.args...)
			out, err := executeRoot(t, args...)
			if err != nil {
				t.Fatalf("spend %v: %v", tc.args, err)
			}
			if strings.HasSuffix(tc.golden, "forecast.json") {
				out = roundFloats(out)
			}
			assertGolden(t, tc.golden, out)
		})
	}
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
