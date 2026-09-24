package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/decide"
	"go.klarlabs.de/tokenops/internal/capability/experiments"
	"go.klarlabs.de/tokenops/internal/capability/explain"
	"go.klarlabs.de/tokenops/internal/capability/learn"
	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func newDecisionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "decision", Short: "Inspect recorded TokenOps decisions"}
	cmd.AddCommand(newDecisionExplainCmd())
	return cmd
}

func newDecisionExplainCmd() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use: "explain <decision-id>", Short: "Explain a decision from its recorded evidence", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, closeStore, err := openControlStore(cmd, dbPath)
			if err != nil {
				return err
			}
			defer closeStore()
			events, err := store.Query(cmd.Context(), sqlite.Filter{Decision: args[0], Limit: 10_000})
			if err != nil {
				return err
			}
			report, ok := explain.Build(args[0], events)
			if !ok {
				return fmt.Errorf("decision %q not found", args[0])
			}
			return writeControlJSON(cmd, report)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db")
	return cmd
}

func newOutcomeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "outcome", Short: "Record outcome evidence for an execution"}
	cmd.AddCommand(newOutcomeRecordCmd(), newOutcomeDetectCmd())
	return cmd
}

func newOutcomeDetectCmd() *cobra.Command {
	var decisionID, sessionID, dbPath string
	cmd := &cobra.Command{
		Use: "detect <execution-id>", Short: "Record the final local verifier result after the last edit", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, ok, err := outcomes.DetectSession(args[0], decisionID, sessionID)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("no recognized verifier found after the final edit; run tests or record a human outcome")
			}
			store, closeStore, err := openControlStore(cmd, dbPath)
			if err != nil {
				return err
			}
			defer closeStore()
			if env.Correlation.Decision != "" {
				history, err := store.Query(cmd.Context(), sqlite.Filter{Decision: env.Correlation.Decision, Limit: 10_000})
				if err != nil {
					return err
				}
				outcomes.CorrelateDecisionLifecycle(env, history)
			}
			if err := store.Append(cmd.Context(), env); err != nil {
				return err
			}
			return writeControlJSON(cmd, env)
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "Claude Code session id")
	cmd.Flags().StringVar(&decisionID, "decision", "", "decision id this outcome evaluates")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db")
	_ = cmd.MarkFlagRequired("session")
	return cmd
}

func newOutcomeRecordCmd() *cobra.Command {
	var result, decisionID, caveat, dbPath string
	var attentionMinutes float64
	cmd := &cobra.Command{
		Use: "record <execution-id>", Short: "Record the operator's assessment", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := parseCLIOutcome(result)
			if err != nil {
				return err
			}
			var attention *float64
			if cmd.Flags().Changed("attention-minutes") {
				if math.IsNaN(attentionMinutes) || math.IsInf(attentionMinutes, 0) || attentionMinutes < 0 {
					return fmt.Errorf("--attention-minutes must be a finite non-negative number")
				}
				attention = &attentionMinutes
			}
			store, closeStore, err := openControlStore(cmd, dbPath)
			if err != nil {
				return err
			}
			defer closeStore()
			env := outcomes.Event(outcomes.Record{ExecutionID: args[0], DecisionID: decisionID, Result: r, Assessment: eventschema.OutcomeHuman, Caveat: caveat, AttentionMinutes: attention})
			if env.Correlation.Decision != "" {
				history, err := store.Query(cmd.Context(), sqlite.Filter{Decision: env.Correlation.Decision, Limit: 10_000})
				if err != nil {
					return err
				}
				outcomes.CorrelateDecisionLifecycle(env, history)
			}
			if err := store.Append(cmd.Context(), env); err != nil {
				return err
			}
			return writeControlJSON(cmd, map[string]any{"event_id": env.ID, "execution_id": args[0], "result": r, "assessment": eventschema.OutcomeHuman})
		},
	}
	cmd.Flags().StringVar(&result, "result", "", "achieved | partial | not_achieved")
	cmd.Flags().StringVar(&decisionID, "decision", "", "decision id this outcome evaluates")
	cmd.Flags().StringVar(&caveat, "caveat", "", "assessment context")
	cmd.Flags().Float64Var(&attentionMinutes, "attention-minutes", 0, "operator-reported active human effort for this execution, in minutes")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db")
	_ = cmd.MarkFlagRequired("result")
	return cmd
}

func newExperimentCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "experiment", Short: "Manage bounded model-routing trials"}
	cmd.AddCommand(newExperimentStartCmd(), newExperimentStatusCmd(), newExperimentStopCmd())
	return cmd
}

func newExperimentStartCmd() *cobra.Command {
	var pairs, days int
	var dbPath string
	cmd := &cobra.Command{
		Use: "start <provider> <baseline-model> <variant-model>", Short: "Explicitly enroll a proxy-backed paired trial", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, closeStore, err := openControlStore(cmd, dbPath)
			if err != nil {
				return err
			}
			defer closeStore()
			state, err := experiments.New(store).Start(cmd.Context(), experiments.StartInput{
				Provider: args[0], BaselineModel: args[1], VariantModel: args[2], MaxPairs: pairs,
				Duration:    time.Duration(days) * 24 * time.Hour,
				Fingerprint: decide.RouteFingerprint(eventschema.Provider(args[0]), args[1], args[2], "proxy"),
			})
			if err != nil {
				return err
			}
			return writeControlJSON(cmd, state)
		},
	}
	cmd.Flags().IntVar(&pairs, "pairs", 10, "matched pairs (1-10)")
	cmd.Flags().IntVar(&days, "days", 14, "maximum duration (1-14 days)")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db")
	return cmd
}

func newExperimentStatusCmd() *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use: "status <experiment-id>", Short: "Show a trial's persisted state", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, closeStore, err := openControlStore(cmd, dbPath)
			if err != nil {
				return err
			}
			defer closeStore()
			manager := experiments.New(store)
			state, ok, err := manager.Status(cmd.Context(), args[0], time.Time{})
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("experiment %q not found", args[0])
			}
			history, err := manager.Events(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return writeControlJSON(cmd, map[string]any{"state": state, "belief": learn.Routing(history, state.Fingerprint, time.Now().UTC())})
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db")
	return cmd
}

func newExperimentStopCmd() *cobra.Command {
	var reason, dbPath string
	cmd := &cobra.Command{
		Use: "stop <experiment-id>", Short: "Stop a trial without deleting evidence", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, closeStore, err := openControlStore(cmd, dbPath)
			if err != nil {
				return err
			}
			defer closeStore()
			manager := experiments.New(store)
			if err := manager.Stop(cmd.Context(), args[0], reason, time.Time{}); err != nil {
				return err
			}
			state, _, err := manager.Status(cmd.Context(), args[0], time.Time{})
			if err != nil {
				return err
			}
			history, err := manager.Events(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return writeControlJSON(cmd, map[string]any{"state": state, "belief": learn.Routing(history, state.Fingerprint, time.Now().UTC())})
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "operator stopped", "why the trial was stopped")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db")
	return cmd
}

func openControlStore(cmd *cobra.Command, override string) (*sqlite.Store, func(), error) {
	path, err := resolvePlanDB(override)
	if err != nil {
		return nil, func() {}, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, func() {}, fmt.Errorf("no event store at %s — run `tokenops init` and start the daemon", path)
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		return nil, func() {}, err
	}
	return store, func() { _ = store.Close() }, nil
}

func parseCLIOutcome(raw string) (eventschema.OutcomeResult, error) {
	r := eventschema.OutcomeResult(raw)
	switch r {
	case eventschema.OutcomeAchieved, eventschema.OutcomePartial, eventschema.OutcomeNotAchieved:
		return r, nil
	default:
		return eventschema.OutcomeUnknown, fmt.Errorf("--result must be achieved, partial, or not_achieved")
	}
}

func writeControlJSON(cmd *cobra.Command, value any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
