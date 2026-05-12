package cli

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

type demoFlags struct {
	storagePath string
	days        int
	perDay      int
	reset       bool
	dryRun      bool
	seed        uint64
}

func newDemoCmd() *cobra.Command {
	f := &demoFlags{}
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Seed synthetic events so spend/burn/forecast/scorecard return populated data",
		Long: `demo writes representative PromptEvents to the sqlite event store so
first-run operators see populated dashboards immediately. Events span
multiple providers, models, workflows, and agents with realistic token
and cost values.

Run after tokenops init. Re-run with --reset to clear and reseed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDemo(cmd.Context(), cmd, f)
		},
	}
	cmd.Flags().StringVar(&f.storagePath, "storage-path", "", "override events.db path (defaults to TOKENOPS_STORAGE_PATH or ~/.tokenops/events.db)")
	cmd.Flags().IntVar(&f.days, "days", 7, "number of days to seed (most recent N)")
	cmd.Flags().IntVar(&f.perDay, "per-day", 40, "approximate events per day")
	cmd.Flags().BoolVar(&f.reset, "reset", false, "delete existing events before seeding")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "report how many events would be seeded without writing")
	cmd.Flags().Uint64Var(&f.seed, "seed", 1, "deterministic RNG seed so re-runs produce identical fixtures")
	return cmd
}

func runDemo(ctx context.Context, cmd *cobra.Command, f *demoFlags) error {
	if f.days <= 0 {
		return fmt.Errorf("--days must be positive, got %d", f.days)
	}
	if f.perDay <= 0 {
		return fmt.Errorf("--per-day must be positive, got %d", f.perDay)
	}

	path, err := resolveDemoStoragePath(f.storagePath)
	if err != nil {
		return err
	}

	envs := generateDemoEnvelopes(f.days, f.perDay, f.seed)
	if f.dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "dry-run: would seed %d events to %s\n", len(envs), path)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	if f.reset {
		if _, err := store.DB().ExecContext(ctx, "DELETE FROM events"); err != nil {
			return fmt.Errorf("reset events: %w", err)
		}
	}

	if err := store.AppendBatch(ctx, envs); err != nil {
		return fmt.Errorf("append events: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(),
		"seeded %d events to %s spanning %d days\nnext: query via `tokenops spend summary --since %dd` or the MCP tools\n",
		len(envs), path, f.days, f.days,
	)
	return nil
}

func resolveDemoStoragePath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if v := os.Getenv("TOKENOPS_STORAGE_PATH"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "events.db"), nil
}

// demoFixture is a small static mix of provider/model/cost shapes that
// give the analytics layer enough diversity to populate top_consumers
// and provider breakdowns without resorting to invented numbers.
type demoFixture struct {
	Provider          eventschema.Provider
	Model             string
	InputCostPerKTok  float64
	OutputCostPerKTok float64
	AvgInputTokens    int64
	AvgOutputTokens   int64
}

var demoFixtures = []demoFixture{
	{"anthropic", "claude-opus-4-7", 15.0 / 1000, 75.0 / 1000, 2400, 800},
	{"anthropic", "claude-sonnet-4-6", 3.0 / 1000, 15.0 / 1000, 1800, 600},
	{"openai", "gpt-4o", 2.5 / 1000, 10.0 / 1000, 2000, 500},
	{"openai", "gpt-4o-mini", 0.15 / 1000, 0.6 / 1000, 1500, 400},
	{"gemini", "gemini-2.5-pro", 1.25 / 1000, 5.0 / 1000, 2200, 700},
}

var demoWorkflows = []string{"code-review", "summarize-pr", "draft-email", "research-loop"}
var demoAgents = []string{"claude-code", "cursor-agent", "internal-rag"}

// generateDemoEnvelopes builds a deterministic event stream sized for
// the analytics surfaces. Seed makes re-runs identical so tests can
// assert on the resulting summaries without flakes. The schema mirrors
// what proxy/observation.go produces in real traffic.
func generateDemoEnvelopes(days, perDay int, seed uint64) []*eventschema.Envelope {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	now := time.Now().UTC()
	envs := make([]*eventschema.Envelope, 0, days*perDay)
	for d := 0; d < days; d++ {
		dayStart := now.AddDate(0, 0, -(days - 1 - d))
		for i := 0; i < perDay; i++ {
			fx := demoFixtures[rng.IntN(len(demoFixtures))]
			// Jitter token counts ±25% so per-request spend isn't a
			// flat line on the dashboard.
			inTok := fx.AvgInputTokens + int64(rng.NormFloat64()*float64(fx.AvgInputTokens)*0.15)
			outTok := fx.AvgOutputTokens + int64(rng.NormFloat64()*float64(fx.AvgOutputTokens)*0.15)
			if inTok < 100 {
				inTok = 100
			}
			if outTok < 50 {
				outTok = 50
			}
			cost := (float64(inTok)/1000)*fx.InputCostPerKTok + (float64(outTok)/1000)*fx.OutputCostPerKTok
			ts := dayStart.Add(time.Duration(rng.IntN(24*60*60)) * time.Second)
			envs = append(envs, &eventschema.Envelope{
				ID:            uuid.NewString(),
				SchemaVersion: eventschema.SchemaVersion,
				Type:          eventschema.EventTypePrompt,
				Timestamp:     ts,
				Source:        "demo",
				Payload: &eventschema.PromptEvent{
					Provider:     fx.Provider,
					RequestModel: fx.Model,
					InputTokens:  inTok,
					OutputTokens: outTok,
					TotalTokens:  inTok + outTok,
					ContextSize:  inTok,
					Latency:      time.Duration(800+rng.IntN(2400)) * time.Millisecond,
					Status:       200,
					CostUSD:      cost,
					WorkflowID:   demoWorkflows[rng.IntN(len(demoWorkflows))],
					AgentID:      demoAgents[rng.IntN(len(demoAgents))],
				},
			})
		}
	}
	return envs
}
