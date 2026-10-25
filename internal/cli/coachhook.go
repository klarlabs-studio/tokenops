package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/bootstrap"
	"go.klarlabs.de/tokenops/internal/capability/budgets"
	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/savings"
	"go.klarlabs.de/tokenops/internal/capability/spending"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/pkg/eventschema"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/routers"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/modellever"
	"go.klarlabs.de/tokenops/internal/infra/readguard"
	"go.klarlabs.de/tokenops/internal/infra/routeguard"
)

// stopHookInput is the JSON Claude Code sends a Stop hook on stdin. Only the
// fields the coach needs are decoded; unknown fields are ignored.
type stopHookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	HookEventName  string `json:"hook_event_name"`
	StopHookActive bool   `json:"stop_hook_active"`

	// SessionIdle is opencode's end-of-turn event name. opencode carries
	// no tokens in the payload and no transcript path — its turns live in
	// its own store, which the handler reads by session id.
	// ConversationID is Cursor's session identifier. Cursor uses the same
	// stop event but names things differently and, crucially, reports the
	// turn's tokens in the payload rather than pointing at a transcript.
	ConversationID string `json:"conversation_id"`
	InputTokens    *int64 `json:"input_tokens"`
	OutputTokens   *int64 `json:"output_tokens"`
}

// isCursorPayload reports whether this stop event came from Cursor.
//
// Detected from the payload rather than from a flag, the same rule the
// transcript dialects follow: one handler, and the operator never has to
// tell it which client invoked it. Cursor is the only one of the three
// that carries token counts inline, and the only one using
// conversation_id.
func (in stopHookInput) isCursorPayload() bool {
	return in.ConversationID != "" && (in.InputTokens != nil || in.OutputTokens != nil)
}

// stopHookOutput is the JSON a Stop hook writes to stdout to surface a
// user-facing, non-blocking message. systemMessage is Claude Code's documented
// channel for a nudge the operator sees; it does NOT block or force the agent
// to continue (which decision:"block" would). suppressOutput keeps the hook's
// own stdout out of the transcript.
type stopHookOutput struct {
	SystemMessage  string `json:"systemMessage"`
	SuppressOutput bool   `json:"suppressOutput"`
}

// newCoachHookCmd is the Claude Code Stop-hook coaching nudge. Its bare form is
// the hook handler (reads the Stop JSON on stdin, accumulates the session's
// API-equivalent spend, and emits a graduated systemMessage nudge as that spend
// crosses fractions of a per-session budget); the `hook` and `stats`
// subcommands install and inspect it.
func newCoachHookCmd(rf *rootFlags) *cobra.Command {
	var (
		budget   float64
		dir      string
		guardDir string
	)
	cmd := &cobra.Command{
		Use:   "coach-hook",
		Short: "Claude Code Stop hook that nudges as a session's cumulative cost crosses budget fractions",
		Long: `coach-hook is a Claude Code Stop hook. Wired onto Stop, it reads the
tail of the session transcript after each turn, sums the full API-equivalent
cost of the new turns — cache-read is the dominant, most reclaimable part, and
it compounds every turn you carry a large context — and, as the session's
cumulative spend crosses fractions of a per-session budget (your own p90
session over the last 30 days once 20 are known, $50 until then),
surfaces graduated, non-blocking nudges to /compact or start a fresh session. It
works for clients that never route through the tokenops proxy, because it acts
inside the client.

On a flat-rate plan (Claude Max, ChatGPT Pro) dollars are a counterfactual
nobody pays, so the coach speaks the plan's live window instead: its share
used, its pace and when it resets. When that reading is lost (an expired
claude.ai session, a daemon not reading), it says so once every few hours
across sessions, with how to get it back, and never falls back to dollars.

Unlike a flat per-turn threshold, a cumulative budget catches the long, flat
sessions where no single turn looks extreme but thousands of turns compound into
real money. Each budget-fraction alert (50%, 75%, 100%, then every additional
budget over) fires once, so the coach never nags every turn; past 100% the further
steps only update the status line. Past coaching.quiet.max_per_day interruptions
a day, tips wait on the status line too; 'tokenops coach tip' answers them.

Bare invocation is the hook handler (reads Stop JSON on stdin). Use
'tokenops coach-hook hook' to print the settings.json block (or prefer
'tokenops hooks install --coach'), and 'tokenops coach stats' to see how
much your sessions have spent and which budget alerts fired.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := coachhook.DefaultConfig()
			cfg.BudgetUSD = budget
			cfg.Enabled = advisoryCoaching(rf)
			cfg.Quiet = quietPolicy(rf)
			cfg.Promotion = promotionNudge(rf, guardDir)
			cfg.Rates = datedRates(rf)
			cfg.Verbosity = coachVerbosity(rf)
			cfg.HoldoutPercent = coachHoldout(rf)
			plan := func(context.Context, eventschema.Provider, time.Time) planReading { return planReading{} }
			if loaded, err := loadConfig(rf); err == nil {
				plan = func(ctx context.Context, p eventschema.Provider, now time.Time) planReading {
					return readPlan(ctx, loaded, p, now)
				}
			}
			return runCoachHook(cmd, dir, cfg, plan)
		},
	}
	cmd.Flags().Float64Var(&budget, "budget", coachhook.DefaultBudgetUSD, "per-session API-equivalent USD budget the alert fractions measure against; the $50 default defers to one derived from your own sessions")
	cmd.Flags().StringVar(&dir, "dir", "", "state/ledger dir (defaults to ~/.tokenops/coach-hook)")
	cmd.Flags().StringVar(&guardDir, "guard-dir", "", "read-guard ledger dir to read the promotion case from (defaults to ~/.tokenops/read-guard)")
	cmd.AddCommand(newCoachHookHookCmd())
	return cmd
}

// runCoachHook reads the Stop JSON, evaluates, and emits a nudge if warranted.
// Errors never disrupt the session — a coach must fail open. On nudge it writes
// {systemMessage, suppressOutput:true}; on no-nudge or any error it exits 0
// with no stdout.
func runCoachHook(cmd *cobra.Command, dir string, cfg coachhook.Config, plan func(context.Context, eventschema.Provider, time.Time) planReading) error {
	body, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return nil // fail open
	}
	var in stopHookInput
	if err := json.Unmarshal(body, &in); err != nil {
		return nil // fail open
	}
	now := time.Now()
	ledger := coachLedger()
	cfg.NewID = coachcap.NewID
	var quieted func(power, kind string) bool
	cfg.Quieted = func(kind string) bool {
		if quieted == nil {
			quieted = coachcap.Quieter(ledger, now)
		}
		return quieted(config.PowerInform, kind)
	}
	cfg.Held = coachcap.Holder(ledger, now)
	cfg.MostlyUnseen = coachcap.MostlyUnseen(ledger, now)
	cfg.BudgetAnswers = func() (int, int) { return coachcap.BudgetAnswers(ledger, now) }
	// The spend budgets' windows, as the daemon last measured them.
	cfg.Budgets = budgets.Current("", now)
	provider, known := hookProvider(in, in.isCursorPayload(), in.HookEventName == "session.idle")
	if known && plan != nil {
		r := plan(cmd.Context(), provider, now)
		cfg.Quota, cfg.FlatPlan, cfg.ReadingLost = r.Quota, r.Flat, r.Lost
		cfg.Alternatives = func() []coachhook.Alternative { return otherPlans(cmd.Context(), plan, provider, now) }
	}
	if known && cfg.CompactAtTokens == 0 {
		cfg.CompactAtTokens = compactTipAt(provider)
	}
	var (
		dec   coachhook.Decision
		extra string
	)
	switch {
	case in.HookEventName == "session.idle":
		// opencode. Its own store holds the turns, so only the id is
		// needed — and "session.idle" is a name no other client sends.
		dbPath, derr := coachhook.OpencodeDB()
		if derr != nil {
			return nil // fail open
		}
		dec = coachhook.EvaluateOpencode(dir, dbPath, in.SessionID, cfg, now)
	case in.isCursorPayload():
		dec = coachhook.EvaluateCursor(dir, body, cfg, now)
	default:
		dec = coachhook.Evaluate(dir, in.SessionID, in.TranscriptPath, cfg, now)
		// Claude Code reads its default model at session start, so the
		// turn end is where it is kept in step with the plan.
		extra = claudeDefaultModel(in, ledger, now)
	}
	coachcap.RecordResolutions(ledger, now, tipResolutions(dec.Resolved))
	via := coachcap.ViaStopHook
	if dec.Ambient {
		via = coachcap.ViaAmbient
	}
	coachcap.RecordTip(ledger, now, dec.OfferID, in.SessionID, dec.TipKind, via)
	recordSavings(savings.DefaultLedger(), now, in.SessionID, dec)
	msg := extra
	if dec.Nudge {
		msg = joinMessages(dec.Message, extra)
	}
	if msg == "" {
		return nil // no nudge: exit 0 with no stdout
	}
	out := stopHookOutput{SystemMessage: msg, SuppressOutput: true}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
}

// claudeDefaultModel reconciles Claude Code's default model for new
// sessions with the plan's pressure (coachcap.ReconcileDefaultModel). It reads only
// local files and fails open: no config, no reading, no change.
func claudeDefaultModel(in stopHookInput, ledger coachcap.Ledger, now time.Time) string {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return ""
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return ""
	}
	paths, err := modellever.DefaultPaths()
	if err != nil || routers.Decides(routers.Sources{}, "", true) {
		return ""
	}
	report := coachcap.Build(cfg)
	pressure, known := routeguard.ReadPressureKnown("", boundaryledger.DefaultDir(), now)
	return coachcap.ReconcileDefaultModel(coachcap.DefaultModelEnv{
		Paths: paths, Rung: report.Effective(config.PowerModels), Verbosity: report.Verbosity,
		Pressure: pressure, Known: known, Session: in.SessionID, SessionModel: latestTranscriptModel(in.TranscriptPath),
		Catalog: routeCatalog(cfg), Candidates: cfg.RoutingCandidates(eventschema.ProviderAnthropic), Ledger: ledger, Now: now,
	})
}

func joinMessages(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n" + b
}

func newCoachHookHookCmd() *cobra.Command {
	var budget float64
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Print the settings.json block to wire coach-hook into Claude Code",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe := "tokenops"
			if p, err := os.Executable(); err == nil {
				exe = p
			}
			block := map[string]any{
				"hooks": map[string]any{
					"Stop": []any{
						map[string]any{
							"hooks": []any{
								map[string]any{
									"type":    "command",
									"command": exe,
									"args":    []string{"coach-hook", "--budget", formatBudget(budget)},
									"timeout": 10,
								},
							},
						},
					},
				},
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			fmt.Fprintf(cmd.ErrOrStderr(), "# Add this to ~/.claude/settings.json (or run `tokenops hooks install --coach`).\n")
			return enc.Encode(block)
		},
	}
	cmd.Flags().Float64Var(&budget, "budget", coachhook.DefaultBudgetUSD, "per-session API-equivalent USD budget")
	return cmd
}

// writeCoachHookStats prints what the Stop hook saw: session spend
// against budget fractions, quota and compact tips, what it held back.
func writeCoachHookStats(out io.Writer, s coachhook.Stats) {
	if s.Events == 0 {
		fmt.Fprintln(out, "No coach-hook activity yet. Install the hook (`tokenops hooks install --coach`) and use Claude Code.")
		return
	}
	fmt.Fprintf(out, "Budget and quota tips — %d turns across %d sessions\n", s.Events, s.DistinctSessions)
	fmt.Fprintf(out, "  est. API-equiv spend: max session ~$%.2f · total ~$%.2f\n", s.MaxCumulativeUSD, s.TotalEstSpendUSD)
	writeSessionBudget(out, s.SessionBudget)
	fmt.Fprintf(out, "  budget alerts fired: %d\n", s.Alerts)
	for _, tier := range []string{"50%", "75%", "100%", "200%", "300%"} {
		if n := s.AlertsByTier[tier]; n > 0 {
			fmt.Fprintf(out, "    %-5s %d\n", tier, n)
		}
	}
	if len(s.UnpricedModels) > 0 {
		fmt.Fprintln(out, "  not priced — no rate card for these models:")
		for _, m := range sortedKeys(s.UnpricedModels) {
			fmt.Fprintf(out, "    %-22s %d turn(s)\n", m, s.UnpricedModels[m])
		}
		fmt.Fprintln(out, "    their spend reads as $0 above. Add rates via `pricing.path` to count them.")
	}
	if s.BurnWarnings > 0 {
		fmt.Fprintf(out, "  run-out warnings (flat plan, both look-backs): %d\n", s.BurnWarnings)
	}
	if c := s.QuotaNudges["100%"]; c > 0 {
		fmt.Fprintf(out, "  used-up windows said: %d\n", c)
	}
	if n := totalOf(s.QuotaNudges) - s.QuotaNudges["100%"]; n > 0 {
		fmt.Fprintf(out, "  share tips from earlier versions (50/75/90%%): %d\n", n)
	}
	writeRunOutScore(out, s.RunOut)
	if s.CompactTips > 0 {
		fmt.Fprintf(out, "  compact-now tips: %d\n", s.CompactTips)
	}
	if s.PromotionNudges > 0 {
		fmt.Fprintf(out, "  read-guard case argued: %d session(s)\n", s.PromotionNudges)
	}
	if n := s.Suppressed["ignored_before"]; n > 0 {
		fmt.Fprintf(out, "  held back because earlier tips like them were ignored: %d\n", n)
	}
	if n := s.Suppressed[coachhook.SuppressedEpisode] + s.Suppressed[coachhook.SuppressedDailyBudget] + s.Suppressed[coachhook.SuppressedMostlyUnseen]; n > 0 {
		fmt.Fprintf(out, "  shown on the status line instead of interrupting: %d\n", n)
		if c := s.Suppressed[coachhook.SuppressedEpisode]; c > 0 {
			fmt.Fprintf(out, "    %-22s %d\n", "already told (episode)", c)
		}
		if c := s.Suppressed[coachhook.SuppressedDailyBudget]; c > 0 {
			fmt.Fprintf(out, "    %-22s %d\n", "daily budget spent", c)
		}
		if c := s.Suppressed[coachhook.SuppressedMostlyUnseen]; c > 0 {
			fmt.Fprintf(out, "    %-22s %d\n", "mostly unseen, said today", c)
		}
	}
	if n := s.Suppressed[coachhook.SuppressedSnoozed] + s.Suppressed[coachhook.SuppressedAnswered]; n > 0 {
		fmt.Fprintf(out, "  held by your answers: %d snoozed, %d already answered\n",
			s.Suppressed[coachhook.SuppressedSnoozed], s.Suppressed[coachhook.SuppressedAnswered])
	}
	if n := s.Suppressed["min_interval"] + s.Suppressed["max_per_session"]; n > 0 {
		fmt.Fprintf(out, "  held back by coaching.quiet: %d\n", n)
		for _, rule := range []string{"min_interval", "max_per_session"} {
			if n := s.Suppressed[rule]; n > 0 {
				fmt.Fprintf(out, "    %-15s %d\n", rule, n)
			}
		}
	}
	switch {
	case s.Alerts == 0 && len(s.UnpricedModels) > 0:
		// "Lean" would be an over-claim: nothing crossed a budget
		// fraction because nothing could be measured against one.
		fmt.Fprintln(out, "\nNo session has crossed a budget fraction — but some turns could not be priced,")
		fmt.Fprintln(out, "so this is not evidence that your spend is lean. Price those models first.")
	case s.Alerts == 0:
		fmt.Fprintln(out, "\nNo session has crossed a budget fraction yet — your spend stays lean. Keep observing.")
	}
}

// writeRunOutScore prints how the run-out forecasts fared once their
// windows reset: precision is forecasts that came true.
func writeRunOutScore(out io.Writer, r coachhook.RunOutScore) {
	if r.Windows == 0 {
		return
	}
	fmt.Fprintf(out, "  windows settled: %d\n", r.Windows)
	if p, ok := r.Precision(); ok {
		fmt.Fprintf(out, "    forecast to run out: %d, of which ran out: %d (precision %.0f%%)\n", r.Forecast, r.ForecastRanOut, p*100)
	}
	if r.RanOutUnforecast > 0 {
		fmt.Fprintf(out, "    ran out without a forecast: %d\n", r.RanOutUnforecast)
	}
}

// writeSessionBudget says what budget sessions are judged against and
// where it came from, so a derived figure is never a mystery number.
func writeSessionBudget(out io.Writer, b *coachhook.SessionBudget) {
	if b == nil {
		return
	}
	fmt.Fprintf(out, "  session budget: %s — %s", wholeUSD(b.BudgetUSD), b.Provenance())
	if b.FromHistory() {
		fmt.Fprintf(out, " in %d days (derived %s)", b.WindowDays, b.DerivedAt.Format("2006-01-02"))
	}
	fmt.Fprintln(out)
	switch {
	case b.DriftClamped():
		fmt.Fprintf(out, "    held from %s: moves at most %d%% a week (was %s)\n", wholeUSD(b.ObservedUSD), int(b.MaxDrift*100+0.5), wholeUSD(b.PreviousUSD))
	case b.BoundClamped():
		fmt.Fprintf(out, "    held from %s at the %s\n", wholeUSD(b.ObservedUSD), b.Clamp)
	}
}

// wholeUSD renders whole dollars without cents ("$45"), cents otherwise.
func wholeUSD(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("$%d", int64(v))
	}
	return fmt.Sprintf("$%.2f", v)
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func totalOf(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// formatBudget renders a budget for the hook args: whole dollars without a
// trailing ".0" ("50"), otherwise the decimal form ("49.5").
func formatBudget(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// advisoryCoaching reports whether the coach may nudge unprompted. The
// nudge never blocks, so it sits on the `advise` rung, which is the
// default — installing the Stop hook is itself the operator asking for
// it, and an upgrade must not quietly take that away.
//
// At `observe` the hook still runs and still records the ledger; it just
// says nothing, which is what makes `coach stats` meaningful before
// you let it speak.
//
// An unreadable config falls back to the default rather than to silence.
// A coach that goes quiet because it could not parse a YAML file is the
// silent-failure shape this tool exists to find.
func advisoryCoaching(rf *rootFlags) bool {
	cfg, err := loadConfig(rf)
	if err != nil {
		cfg = config.Default()
	}
	return coachcap.Build(cfg).Effective(config.PowerInform) != config.AutonomyOff
}

// coachVerbosity is how much the coach says (ADR 0006). An unreadable
// config reads as normal: today's behaviour, never silence.
// coachHoldout is the share of sessions whose cost and drift tips are
// held back to measure their saving; an unreadable config holds nothing.
func coachHoldout(rf *rootFlags) int {
	cfg, err := loadConfig(rf)
	if err != nil {
		return 0
	}
	return coachcap.Build(cfg).HoldoutPercent
}

// recordSavings writes this Stop's tip, shown or held back, to the
// realized-savings ledger with its expected saving and evidence, and the
// implicit outcomes of earlier ones.
func recordSavings(l savings.Ledger, now time.Time, session string, dec coachhook.Decision) {
	tip := savings.Tip{Session: session, ExpectedUSD: dec.ExpectedUSD, ExpectedTokens: dec.ExpectedTokens,
		Evidence: dec.Evidence, SessionUSD: dec.CumulativeUSD}
	if dec.OfferID != "" {
		tip.ID, tip.Kind, tip.Cohort = dec.OfferID, dec.TipKind, savings.Treated
		savings.RecordTip(l, now, tip)
	}
	if dec.ShadowID != "" {
		tip.ID, tip.Kind, tip.Cohort = dec.ShadowID, dec.ShadowKind, savings.Shadow
		if dec.Holdout {
			tip.Cohort = savings.Holdout
		}
		savings.RecordTip(l, now, tip)
	}
	resolved := make([]savings.Resolution, 0, len(dec.Resolved)+len(dec.ShadowResolved))
	for _, r := range append(append([]coachhook.TipResolution{}, dec.Resolved...), dec.ShadowResolved...) {
		resolved = append(resolved, savings.Resolution{ID: r.ID, Signal: r.Signal, Followed: r.Followed, Evidence: r.Evidence})
	}
	savings.RecordOutcomes(l, now, resolved)
}

func coachVerbosity(rf *rootFlags) string {
	cfg, err := loadConfig(rf)
	if err != nil {
		return config.VerbosityNormal
	}
	return coachcap.Build(cfg).Verbosity
}

// promotionNudge builds the case for letting the read guard start
// blocking, or returns empty when nobody should hear it.
//
// Who pulls the trigger follows `coaching.delivery` itself rather than a
// second knob, because it is the same question that ladder already
// answers:
//
//	observe    — record the evidence, say nothing
//	advise     — make the case, wait for the human
//	intervene  — already there
//
// So `advise` is the only level that argues. At `observe` the coach is
// silent by construction; at `intervene` the guard is already refusing
// re-reads and there is nothing left to ask for.
func promotionNudge(rf *rootFlags, guardDir string) string {
	cfg, err := loadConfig(rf)
	if err != nil {
		// An unreadable config resolves to advise elsewhere in this file,
		// but silence is the right failure here: the case is a standing
		// one that will be just as true after the config is fixed, and
		// arguing for an intervention on a config we could not read is
		// the wrong direction to fail in.
		return ""
	}
	// Only a coach that advises about waste argues for acting on it: off
	// says nothing, and autonomous already refuses the re-reads.
	if coachcap.Build(cfg).Effective(config.PowerWaste) != config.AutonomyAdvise {
		return ""
	}
	stats, err := readguard.ReadStats(guardDir)
	if err != nil {
		return ""
	}
	return promotionCase(stats)
}

// datedRates resolves the rate card the rest of tokenops prices with:
// the embedded baseline, plus the snapshots under ~/.tokenops/pricing,
// plus any negotiated-rate override — effective-dated, so a turn is
// priced at the card in force when it ran.
//
// This package used the embedded baseline alone until it turned out that
// is not the same card. A machine whose snapshot knew gpt-5.5 still had
// its coach-hook budget measured against a baseline that did not, so no
// tier could fire and the session read as free.
//
// Nil on any failure, which falls back to the baseline: a coach that
// cannot price is still better than a hook that refuses to run.
func datedRates(rf *rootFlags) func(time.Time) spending.Table {
	path := ""
	if cfg, err := loadConfig(rf); err == nil {
		path = cfg.Pricing.Path
	}
	if eng := hookSpendEngine(path); eng != nil {
		return eng.TableAt
	}
	return nil
}

// hookSpendEngine is the cost engine with the negotiated rates at path,
// nil when none can be built. A malformed override file prices without
// it: a hook must not refuse to run over the operator's YAML.
func hookSpendEngine(path string) *spending.Engine {
	eng, err := bootstrap.SpendEngine(path, nil)
	if err != nil {
		eng, err = bootstrap.SpendEngine("", nil)
	}
	if err != nil {
		return nil
	}
	return eng
}

// quietPolicy reads coaching.quiet into the hook's rate limit.
//
// An unreadable config yields the zero policy — no floor, no cap — for
// the same reason advisoryCoaching falls back to speaking: a coach that
// silences itself because it could not parse a YAML file is the
// silent-failure shape this tool exists to find. The per-finding latches
// still apply, so the fallback is the behaviour that shipped before the
// key existed, not an unbounded one.
func quietPolicy(rf *rootFlags) coachhook.Quiet {
	cfg, err := loadConfig(rf)
	if err != nil {
		return coachhook.Quiet{}
	}
	return coachhook.Quiet{
		MinInterval:   cfg.Coaching.Quiet.MinInterval,
		MaxPerSession: cfg.Coaching.Quiet.MaxPerSession,
		MaxPerDay:     cfg.Coaching.Quiet.InterruptionsPerDay(),
	}
}

func tipResolutions(rs []coachhook.TipResolution) []coachcap.Resolution {
	out := make([]coachcap.Resolution, 0, len(rs))
	for _, r := range rs {
		out = append(out, coachcap.Resolution{ID: r.ID, Kind: r.Kind, Followed: r.Followed, Evidence: r.Evidence})
	}
	return out
}

// compactAt is where the live compact tip's stretch starts for a client:
// the same line the compact_earlier finding uses for that client's
// workflows, including an operator's coaching.context_limits override.
//
// The tip is the context power's advise rung: off says nothing, and
// autonomous has the agent compact at that line itself, so the tip would
// only repeat what is already happening.
func compactTipAt(provider eventschema.Provider) int64 {
	prefix := "claude-code:"
	if provider == eventschema.ProviderOpenAI {
		prefix = "codex:"
	}
	var cfg config.Config
	if path, err := config.DefaultPath(); err == nil {
		if loaded, err := config.Load(path); err == nil {
			cfg = loaded
		}
	}
	if coachcap.Build(cfg).Effective(config.PowerContext) != config.AutonomyAdvise {
		return 0
	}
	return coachcap.CompactAt(cfg, prefix)
}
