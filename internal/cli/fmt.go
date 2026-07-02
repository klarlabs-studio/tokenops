package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/config"
	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/formatter"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// newFmtCmd assembles `tokenops fmt` — the deterministic command-output
// compressor. It runs a wrapped command, compresses its stdout to a compact
// form before that output enters the agent context, and preserves the full
// output in a local recovery store so nothing is ever lost.
//
//	tokenops fmt -- git status
//	tokenops fmt --level aggressive -- docker ps
//
// The wrapped command's exit code is propagated verbatim so an agent still
// sees failures. stderr is passed through unchanged (errors are always
// critical); only stdout is compressed.
func newFmtCmd(rf *rootFlags) *cobra.Command {
	var (
		levelFlag  string
		recoverDir string
		noRecover  bool
		quiet      bool
		rawOnError bool
		statsJSON  bool
		emitFlag   bool
		dbFlag     string
	)
	cmd := &cobra.Command{
		Use:   "fmt [flags] -- <command> [args...]",
		Short: "Run a command and compress its output deterministically before it reaches the agent",
		Long: `fmt wraps a shell command, compresses its stdout with a
deterministic per-command formatter, and forwards the compact result. Every
line the formatter classifies as critical (errors, failures, changed state)
is preserved verbatim; only noise is removed. The full raw output is written
to a recovery file (~/.tokenops/recovery/) so detail is always retrievable.

Loss level is configured per command in config (optimizer.command_fmt) and
can be overridden for a single run with --level.

Examples:
  tokenops fmt -- git status
  tokenops fmt --level aggressive -- npm install
  tokenops fmt --quiet -- go build ./...`,
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Config is optional for fmt; fall back to defaults on error.
			cfg, err := loadConfig(rf)
			if err != nil {
				cfg = config.Config{}
			}
			policy, warn := buildLossPolicy(cfg.Optimizer.CommandFmt, levelFlag)
			if warn != "" && !quiet {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warn)
			}
			formatters, fwarns := allFormatters(cfg.Optimizer.CommandFmt)
			if !quiet {
				for _, w := range fwarns {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
				}
			}
			reg := formatter.NewRegistry(policy, formatters...)

			res, err := runFmt(cmd.Context(), reg, args, fmtOptions{
				RecoverDir: recoverDir,
				NoRecover:  noRecover,
				RawOnError: rawOnError,
			})
			if err != nil {
				return err
			}

			// Forward compact stdout and raw stderr.
			_, _ = cmd.OutOrStdout().Write(res.Stdout)
			_, _ = cmd.ErrOrStderr().Write(res.Stderr)

			// Emit an OptimizationEvent so the dashboard/scorecard count
			// the savings. Opt-in (flag or config), best-effort.
			if (emitFlag || cfg.Optimizer.CommandFmt.EmitEvents) && res.Compressed {
				if err := emitFmtEvent(cmd.Context(), dbFlag, args, res); err != nil && !quiet {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: fmt event not recorded: %v\n", err)
				}
			}

			// Append a learn record (best-effort) so `tokenops fmt learn`
			// can mine next-formatter priorities + over-compression.
			command := firstArgvToken(args)
			_ = recordCompressRun(recoverDir, command, policy.LevelFor(command).String(), res, time.Now())

			if !quiet {
				printFmtStats(cmd, res, statsJSON)
			}

			// Propagate the child's exit code so agents see failures.
			if res.ExitCode != 0 {
				os.Exit(res.ExitCode)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&levelFlag, "level", "", "override loss level for this run (conservative|balanced|aggressive)")
	cmd.Flags().StringVar(&recoverDir, "recover-dir", "", "recovery store dir (defaults to ~/.tokenops/recovery)")
	cmd.Flags().BoolVar(&noRecover, "no-recover", false, "do not persist full output to the recovery store")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "suppress the savings/recovery stats line")
	cmd.Flags().BoolVar(&rawOnError, "raw-on-error", true, "forward raw (uncompressed) stdout when the command exits non-zero")
	cmd.Flags().BoolVar(&statsJSON, "stats-json", false, "emit the stats line as JSON on stderr")
	cmd.Flags().BoolVar(&emitFlag, "emit", false, "append an OptimizationEvent to the events store (also set via config command_fmt.emit_events)")
	cmd.Flags().StringVar(&dbFlag, "db", "", "events.db path for --emit (defaults to ~/.tokenops/events.db)")
	cmd.AddCommand(newFmtBenchCmd(rf))
	cmd.AddCommand(newFmtHookCmd(rf))
	cmd.AddCommand(newFmtRecoverCmd())
	cmd.AddCommand(newFmtLearnCmd())
	return cmd
}

// registryFormatters resolves the built-in + config formatter set for the
// given root flags, ignoring config-load errors (defaults still apply).
func registryFormatters(rf *rootFlags) []formatter.Formatter {
	cfg, err := loadConfig(rf)
	if err != nil {
		return formatter.DefaultFormatters()
	}
	formatters, _ := allFormatters(cfg.Optimizer.CommandFmt)
	return formatters
}

// emitFmtEvent appends an OptimizationEvent (kind=command_fmt) to the local
// events store so the dashboard and scorecard attribute the savings. It is
// best-effort: a missing store or write error is returned for an optional
// warning but never aborts the wrapped command. The workflow/agent id is
// stamped "fmt:<command>" so `group=agent` can show which commands compress
// most.
func emitFmtEvent(ctx context.Context, dbPath string, argv []string, res *fmtResult) error {
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dbPath = filepath.Join(home, ".tokenops", "events.db")
	}
	// Ensure the parent dir exists; sqlite.Open creates the db + schema
	// when absent, so an explicit `--emit` works before `tokenops start`.
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}
	store, err := sqlite.Open(ctx, dbPath, sqlite.Options{})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	command := firstArgvToken(argv)
	saved := int64(estTokens(res.BytesBefore - res.BytesAfter))
	env := &eventschema.Envelope{
		ID:            uuid.NewString(),
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypeOptimization,
		Timestamp:     time.Now().UTC(),
		Source:        "tokenops-fmt",
		Payload: &eventschema.OptimizationEvent{
			Kind:                   eventschema.OptimizationTypeCommandFmt,
			Mode:                   eventschema.OptimizationModeInteractive,
			EstimatedSavingsTokens: saved,
			QualityScore:           1.0, // deterministic, critical-line-preserving
			Decision:               eventschema.OptimizationDecisionApplied,
			Reason:                 res.Notes,
			WorkflowID:             "fmt:" + command,
			AgentID:                "fmt:" + command,
		},
	}
	return store.Append(ctx, env)
}

// firstArgvToken returns argv[0]'s base name (path stripped).
func firstArgvToken(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	t := argv[0]
	if i := strings.LastIndexAny(t, "/\\"); i >= 0 {
		t = t[i+1:]
	}
	return t
}

// allFormatters returns the built-in catalog plus any user-defined config
// formatters. Config formatters appear AFTER built-ins so a user command
// that collides with a built-in overrides it (later registration wins in
// the registry map). Invalid user specs are skipped with a warning rather
// than failing the whole run.
func allFormatters(cfg config.CommandFmtConfig) ([]formatter.Formatter, []string) {
	out := formatter.DefaultFormatters()
	var warns []string
	for _, fc := range cfg.Formatters {
		spec := formatter.ConfigSpec{
			Command:  fc.Command,
			Aliases:  fc.Aliases,
			Critical: fc.Critical,
			Drop: map[formatter.LossLevel][]string{
				formatter.LossBalanced:   fc.Drop.Balanced,
				formatter.LossAggressive: fc.Drop.Aggressive,
			},
		}
		f, err := formatter.NewConfigFormatter(spec)
		if err != nil {
			warns = append(warns, err.Error())
			continue
		}
		out = append(out, f)
	}
	return out, warns
}

// buildLossPolicy maps the config strings into the domain LossPolicy and
// applies an optional single-run level override. It returns a human-
// readable warning when a configured token is invalid (the offending entry
// falls back to conservative).
func buildLossPolicy(cfg config.CommandFmtConfig, override string) (formatter.LossPolicy, string) {
	var warns []string
	def, ok := formatter.ParseLossLevel(cfg.Default)
	if !ok && cfg.Default != "" {
		warns = append(warns, fmt.Sprintf("invalid command_fmt.default %q, using conservative", cfg.Default))
	}
	overrides := make(map[string]formatter.LossLevel, len(cfg.Overrides))
	for cmdTok, lvl := range cfg.Overrides {
		parsed, ok := formatter.ParseLossLevel(lvl)
		if !ok {
			warns = append(warns, fmt.Sprintf("invalid command_fmt.overrides[%s]=%q, using conservative", cmdTok, lvl))
		}
		overrides[strings.ToLower(cmdTok)] = parsed
	}
	if override != "" {
		lvl, ok := formatter.ParseLossLevel(override)
		if !ok {
			warns = append(warns, fmt.Sprintf("invalid --level %q, using conservative", override))
		}
		// A run-level override replaces the default AND clears per-command
		// overrides for the run — the operator asked for this level.
		def = lvl
		overrides = nil
	}
	return formatter.LossPolicy{Default: def, Overrides: overrides}, strings.Join(warns, "; ")
}

// fmtOptions carries the recovery/behaviour switches into runFmt.
type fmtOptions struct {
	RecoverDir string
	NoRecover  bool
	RawOnError bool
}

// fmtResult is the outcome of a wrapped run.
type fmtResult struct {
	Stdout       []byte // compact (or raw when compression was declined)
	Stderr       []byte // passed through verbatim
	ExitCode     int
	BytesBefore  int
	BytesAfter   int
	LinesDropped int
	Compressed   bool // net reduction achieved by a command formatter
	Handled      bool // a dedicated command formatter ran (not the generic fallback)
	CriticalKept bool
	RecoveryPath string
	RecoveryID   string // basename of RecoveryPath without extension; links learn records
	Notes        string
}

// runFmt executes argv, captures stdout/stderr, compresses stdout via reg,
// and (unless disabled) writes the full raw output to the recovery store.
// It never returns an error for a non-zero child exit — that is reported in
// fmtResult.ExitCode — only for failures to launch the process.
func runFmt(ctx context.Context, reg *formatter.Registry, argv []string, opt fmtOptions) (*fmtResult, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("fmt: no command given")
	}
	var stdout, stderr bytes.Buffer
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	c.Stdout = &stdout
	c.Stderr = &stderr
	c.Stdin = os.Stdin

	runErr := c.Run()
	exitCode := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			// Failed to start (command not found, permission): surface it.
			return nil, fmt.Errorf("fmt: run %q: %w", argv[0], runErr)
		}
	}

	rawStdout := stdout.Bytes()
	res := &fmtResult{
		Stderr:      stderr.Bytes(),
		ExitCode:    exitCode,
		BytesBefore: len(rawStdout),
		BytesAfter:  len(rawStdout),
		Stdout:      rawStdout,
	}

	// Recovery: persist the full raw output first, so it exists before we
	// hand a compacted view to the agent.
	if !opt.NoRecover && len(rawStdout)+len(res.Stderr) > 0 {
		path, err := writeRecovery(opt.RecoverDir, argv, rawStdout, res.Stderr, exitCode)
		if err == nil {
			res.RecoveryPath = path
			res.RecoveryID = recoveryID(path)
		}
	}

	// On failure, optionally forward raw stdout so the agent sees full
	// diagnostic detail.
	if exitCode != 0 && opt.RawOnError {
		res.Notes = "raw forwarded (non-zero exit)"
		return res, nil
	}

	fr, handled := reg.Format(argv, rawStdout)
	res.Stdout = ensureTrailingNewline(fr.Compact)
	res.BytesAfter = fr.BytesAfter
	res.LinesDropped = fr.LinesDropped
	res.Handled = handled
	res.Compressed = handled && fr.CriticalKept && fr.BytesAfter < fr.BytesBefore
	res.CriticalKept = fr.CriticalKept
	res.Notes = fr.Notes
	return res, nil
}

// recoveryID derives the learn-record ID from a recovery file path: its
// base name without the .out extension.
func recoveryID(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// writeRecovery persists the full raw output to a recovery file and returns
// its path. The file name embeds a short content hash so re-runs of the
// same command output are idempotent and easy to correlate.
func writeRecovery(dir string, argv []string, stdout, stderr []byte, exitCode int) (string, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".tokenops", "recovery")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sum := sha256.Sum256(append(append([]byte(strings.Join(argv, " ")), stdout...), stderr...))
	name := fmt.Sprintf("%s-%x.out", time.Now().UTC().Format("20060102T150405"), sum[:6])
	path := filepath.Join(dir, name)

	var b bytes.Buffer
	fmt.Fprintf(&b, "# tokenops fmt recovery\n# command: %s\n# exit: %d\n# saved: %s\n\n",
		strings.Join(argv, " "), exitCode, time.Now().UTC().Format(time.RFC3339))
	if len(stdout) > 0 {
		b.WriteString("## stdout\n")
		b.Write(stdout)
		b.WriteByte('\n')
	}
	if len(stderr) > 0 {
		b.WriteString("## stderr\n")
		b.Write(stderr)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// printFmtStats writes the savings/recovery summary to stderr so it never
// contaminates the compacted stdout an agent parses.
func printFmtStats(cmd *cobra.Command, res *fmtResult, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(cmd.ErrOrStderr())
		_ = enc.Encode(map[string]any{
			"bytes_before":  res.BytesBefore,
			"bytes_after":   res.BytesAfter,
			"tokens_saved":  estTokens(res.BytesBefore - res.BytesAfter),
			"lines_dropped": res.LinesDropped,
			"compressed":    res.Compressed,
			"critical_kept": res.CriticalKept,
			"recovery_path": res.RecoveryPath,
			"exit_code":     res.ExitCode,
			"notes":         res.Notes,
		})
		return
	}
	saved := res.BytesBefore - res.BytesAfter
	if saved <= 0 {
		if res.RecoveryPath != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "tokenops fmt: no savings (%s); recovery: %s\n", res.Notes, res.RecoveryPath)
		}
		return
	}
	pct := 0.0
	if res.BytesBefore > 0 {
		pct = 100 * float64(saved) / float64(res.BytesBefore)
	}
	recover := res.RecoveryPath
	if res.RecoveryID != "" {
		// Point at the recover verb so a re-fetch is logged as a learning
		// signal (possible critical-line miss) rather than a silent read.
		recover = "tokenops fmt recover " + res.RecoveryID
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"tokenops fmt: saved ~%d tokens (%.0f%% of stdout, %d lines) · full: %s\n",
		estTokens(saved), pct, res.LinesDropped, recover)
}

// ensureTrailingNewline appends a newline to non-empty compact output so
// downstream readers (and the terminal) see a cleanly terminated stream
// without the stderr stats line colliding on the last row.
func ensureTrailingNewline(b []byte) []byte {
	if len(b) == 0 || b[len(b)-1] == '\n' {
		return b
	}
	return append(b, '\n')
}

// estTokens is the byte→token approximation used for the stats line. It is
// deliberately conservative (4 bytes/token) and clearly an estimate.
func estTokens(byteDelta int) int {
	if byteDelta <= 0 {
		return 0
	}
	return byteDelta / 4
}
