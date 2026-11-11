package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/capability/findings"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/mcp"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

func newScorecardCmd(rf *rootFlags) *cobra.Command {
	var jsonOut, local bool
	var baselineRef string
	var dbPath string
	var sinceDays int
	var fvtOverride float64
	var teuOverride float64
	var sacOverride float64

	cmd := &cobra.Command{
		Use:   "scorecard",
		Short: "The one grade: how your sessions went, graded A–F",
		Long: `scorecard is TokenOps' one grade (ADR 0018): the worst of the measures
it grades, with the measure that set it.

Graded:
  session quality             the worst of ` + "`tokenops dx`" + `'s grades over your own
                              instructions (the last 7 days)
  cache hits                  share of input read from the prompt cache
  prompts that only confirm   yes / no / continue, which a clearer
                              instruction could have spared
  replies you rejected        instructions that reject the last reply
  tool calls that succeeded   calls that returned without an error
  destructive commands        rm -rf, force-push, drop table
  optimizer savings           only where the proxy carries traffic

Reported, not graded: first prompt after a session starts, prompts
with a workflow or session id, plan use, unit costs and allocation.

Each measured KPI is set against your own trailing baseline, the 28
days before the window, with whether it moved better or worse given
which way the KPI is healthy. Plan use and unit costs are compared
against the same baseline. It is you against yourself only.

The running daemon keeps what a scorecard reads between requests, so
scorecard asks it. With --local, --db, an override (--fvt-seconds,
--teu-pct, --sac-pct), or no daemon answering, it computes the
scorecard here.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !local && dbPath == "" && fvtOverride == 0 && teuOverride == 0 && sacOverride == 0 {
				if s, ok := scorecardFromDaemon(cmd.Context(), rf, sinceDays); ok {
					s.BaselineRef = baselineRef
					return printScorecard(cmd, s, jsonOut)
				}
			}
			store, closeStore, err := openScorecardStore(cmd.Context(), rf, dbPath)
			if err != nil {
				return err
			}
			defer closeStore()
			// Unit costs price the turns; without a readable config the
			// scorecard is graded as before and they are left off.
			var (
				eng   *spending.Engine
				rules []allocation.Rule
			)
			if cfg, err := loadConfig(rf); err == nil {
				eng, _ = buildSpendEngine(cfg)
				rules = cfg.AllocationRules()
			}
			s := spending.Scorecard(cmd.Context(), store, spending.ScorecardParams{
				SinceDays:   sinceDays,
				FVTSeconds:  fvtOverride,
				TEUPct:      teuOverride,
				SACPct:      sacOverride,
				BaselineRef: baselineRef,
				Spend:       eng,
				Allocation:  rules,
				Sessions:    findings.ScorecardSessions(sinceDays, time.Now()),
			})
			return printScorecard(cmd, s, jsonOut)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().StringVar(&baselineRef, "baseline-ref", "", "reference identifier for the baseline (version, date, or label)")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to ~/.tokenops/events.db)")
	cmd.Flags().IntVar(&sinceDays, "days", 7, "window in days")
	cmd.Flags().Float64Var(&fvtOverride, "fvt-seconds", 0, "override First-Value Time in seconds")
	cmd.Flags().Float64Var(&teuOverride, "teu-pct", 0, "override Token Efficiency Uplift in percent")
	cmd.Flags().Float64Var(&sacOverride, "sac-pct", 0, "override Spend Attribution Completeness in percent")
	cmd.Flags().BoolVar(&local, "local", false, "compute the scorecard here instead of asking the running daemon")
	return cmd
}

func printScorecard(cmd *cobra.Command, s *spending.ScorecardReport, jsonOut bool) error {
	if jsonOut {
		data, err := s.MarshalJSON()
		if err != nil {
			return fmt.Errorf("marshal scorecard: %w", err)
		}
		cmd.Println(string(data))
		return nil
	}
	cmd.Print(s.String())
	return nil
}

// scorecardDaemonWait bounds asking the daemon for a scorecard. Its first
// one after a start reads weeks of transcripts and git history; here they
// would be read again, from nothing, so a slow daemon is still the faster
// answer.
const scorecardDaemonWait = 3 * time.Minute

// scorecardFromDaemon is the scorecard the daemon computes, and whether it
// answered: it keeps the events, the transcripts it read and the
// repositories' history between scorecards, which this process would read
// in full.
func scorecardFromDaemon(ctx context.Context, rf *rootFlags, days int) (*spending.ScorecardReport, bool) {
	cfg, err := loadConfig(rf)
	if err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, scorecardDaemonWait)
	defer cancel()
	var s spending.ScorecardReport
	path := "/api/scorecard?since_days=" + strconv.Itoa(days)
	if err := mcp.DaemonReader(mcp.ConfiguredDaemonURL(cfg), scorecardDaemonWait)(ctx, path, &s); err != nil {
		return nil, false
	}
	return &s, true
}

// openScorecardStore opens the event store read for the live KPIs. A
// fresh install has none yet, which is not an error: the scorecard
// reports warming up. Opening would create an empty database, so a
// missing file returns a nil store instead.
func openScorecardStore(ctx context.Context, rf *rootFlags, flagPath string) (*sqlite.Store, func(), error) {
	path, err := resolveAuditDB(rf, flagPath)
	if err != nil {
		return nil, func() {}, err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, func() {}, nil
	}
	openCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	store, err := openStoreForRead(openCtx, path)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open events db: %w", err)
	}
	return store, func() { _ = store.Close() }, nil
}
