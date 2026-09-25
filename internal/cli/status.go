package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/sourceprobe"
	"go.klarlabs.de/tokenops/internal/presentation"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/internal/version"
)

// httpDoer is the small subset of *http.Client status uses; tests inject a
// fake to avoid binding a real socket.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// statusClient is overridable in tests via SetStatusClient. Production uses
// a tight 2s timeout to keep `tokenops status` responsive.
var statusClient httpDoer = &http.Client{Timeout: 2 * time.Second}

// SetStatusClient injects an httpDoer for tests. It is exported but resides
// in the cli package; production callers do not need it.
func SetStatusClient(c httpDoer) { statusClient = c }

func newStatusCmd(rf *rootFlags) *cobra.Command {
	var (
		addr     string
		jsonOut  bool
		insecure bool
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show daemon health",
		Long:  "status queries the daemon's /healthz, /readyz, and /version endpoints.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target := addr
			cfg, cfgErr := loadConfig(rf)
			if target == "" {
				if cfgErr != nil {
					return cfgErr
				}
				target = cfg.Listen
			}
			scheme := "http"
			if insecure {
				scheme = "https"
			}
			base := scheme + "://" + target

			res, err := fetchStatus(cmd.Context(), base)
			if err != nil {
				// Daemon unreachable. Fall back to the same self-report
				// the MCP tokenops_status tool emits — config + blockers
				// + next_actions — so operators see something actionable
				// instead of an opaque "connection refused". The offline
				// path can't open the store, so it omits warnings.
				return writeOfflineStatus(cmd.OutOrStdout(), base, cfg, cfgErr, jsonOut)
			}
			// Runtime ingestion staleness mirrors the MCP tokenops_status
			// warning. Best-effort: reads the local event store directly
			// (like `vendor-usage status`); any failure degrades to "no
			// warnings" rather than failing the status command.
			if cfgErr == nil {
				res.Warnings = statusStaleWarnings(cmd.Context(), rf, cfg)
			}
			// Dropped rows come from the daemon itself rather than the
			// store — the store is precisely where they failed to land,
			// so it cannot be asked how many are missing.
			if w := dropWarningFrom(res.Health); w != "" {
				res.Warnings = append(res.Warnings, w)
			}
			res.Insight = presentation.ForStatus(statusState(res))
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(res)
			}
			return writeStatusText(cmd.OutOrStdout(), base, res)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "", "daemon host:port (defaults to config.listen)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().BoolVar(&insecure, "https", false, "use https scheme (for TLS-enabled daemons)")
	return cmd
}

// statusResult is the structured status payload returned by `--json`.
type statusResult struct {
	Health   endpointResult             `json:"health"`
	Ready    endpointResult             `json:"ready"`
	Version  endpointResult             `json:"version"`
	Insight  presentation.StatusInsight `json:"insight"`
	Warnings []string                   `json:"warnings,omitempty"`
}

// statusState trusts the daemon's explicit /readyz status and treats local
// ingestion/drop warnings as reduced coverage. Missing or unrecognized
// readiness data stays unavailable rather than being inferred from HTTP 200.
func statusState(r statusResult) string {
	state, _ := r.Ready.Body["status"].(string)
	switch state {
	case "ready":
		if len(r.Warnings) > 0 {
			return "degraded"
		}
		return "ready"
	case "not_configured":
		return "not_configured"
	default:
		return "not_ready"
	}
}

type endpointResult struct {
	URL    string         `json:"url"`
	Status int            `json:"status"`
	Body   map[string]any `json:"body,omitempty"`
	Error  string         `json:"error,omitempty"`
}

func fetchStatus(ctx context.Context, base string) (statusResult, error) {
	res := statusResult{
		Health:  fetchEndpoint(ctx, base+"/healthz"),
		Ready:   fetchEndpoint(ctx, base+"/readyz"),
		Version: fetchEndpoint(ctx, base+"/version"),
	}
	if res.Health.Error != "" && res.Ready.Error != "" && res.Version.Error != "" {
		return res, fmt.Errorf("daemon unreachable at %s: %s", base, res.Health.Error)
	}
	return res, nil
}

func fetchEndpoint(ctx context.Context, url string) endpointResult {
	out := endpointResult{URL: url}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	resp, err := statusClient.Do(req)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer func() { _ = resp.Body.Close() }()
	out.Status = resp.StatusCode

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if len(body) == 0 {
		return out
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		out.Body = map[string]any{"raw": strings.TrimSpace(string(body))}
		return out
	}
	out.Body = parsed
	return out
}

// writeOfflineStatus renders the same shape the MCP tokenops_status
// tool emits when no daemon is reachable. Operators get blockers +
// next_actions and an explanation that the daemon is not running —
// not a connection-refused string they have to interpret.
func writeOfflineStatus(w io.Writer, base string, cfg config.Config, cfgErr error, jsonOut bool) error {
	payload := map[string]any{
		"status":  "daemon_unreachable",
		"daemon":  base,
		"ready":   false,
		"state":   "not_running",
		"insight": presentation.ForStatus("not_running"),
		"hint":    "daemon not running; " + offlineRemedy(daemonSupervised()) + ". MCP-only deployments can ignore this and call `tokenops_status` via the MCP host instead.",
		"version": version.String(),
	}
	if cfgErr == nil {
		blockers := cfg.Blockers()
		payload["blockers"] = blockers
		payload["next_actions"] = config.NextActionsFor(blockers)
	} else {
		payload["config_error"] = cfgErr.Error()
	}
	if jsonOut {
		return json.NewEncoder(w).Encode(payload)
	}
	fmt.Fprintf(w, "daemon: %s (not running)\n", base)
	insight := presentation.ForStatus("not_running")
	fmt.Fprintf(w, "status: %s — %s\n", insight.Level, insight.Summary)
	if cfgErr == nil {
		blockers := cfg.Blockers()
		if len(blockers) == 0 {
			fmt.Fprintln(w, "  blockers: none — "+offlineRemedy(daemonSupervised()))
		} else {
			fmt.Fprintf(w, "  blockers: %s\n", strings.Join(blockers, ", "))
			for _, action := range config.NextActionsFor(blockers) {
				fmt.Fprintf(w, "  next: %s\n", action)
			}
		}
	} else {
		fmt.Fprintf(w, "  config error: %v\n", cfgErr)
	}
	fmt.Fprintf(w, "  version: %s\n", version.String())
	fmt.Fprintln(w, "  hint: "+offlineRemedy(daemonSupervised())+", or query `tokenops_status` via your MCP host for the serve-side view.")
	return nil
}

// statusStaleWarnings computes ingestion-staleness warnings by reading
// the local event store directly, mirroring the MCP tokenops_status
// tool. Best-effort by design: an unresolvable DB path, an unopenable
// store, or a count error all degrade to "no warnings" so the status
// command never fails on the health check. Returns nil when nothing is
// stale.
func statusStaleWarnings(ctx context.Context, rf *rootFlags, cfg config.Config) []string {
	dbPath, err := resolveAuditDB(rf, "")
	if err != nil {
		return nil
	}
	store, err := sqlite.Open(ctx, dbPath, sqlite.Options{})
	if err != nil {
		return nil
	}
	defer func() { _ = store.Close() }()
	var warnings []string
	stale, err := cfg.CheckStaleIngestion(ctx, store, sourceprobe.All(cfg), config.StaleIngestionWindow, time.Now())
	if err == nil {
		for _, s := range stale {
			warnings = append(warnings, s.Warning())
		}
	}
	// A retention rule naming a source that has never written an event is
	// doing nothing, silently. The tag is not always what the operator
	// sees — Cursor's ledger lives in ~/.tokenops/cursor-turns while its
	// events are stamped cursor-hook — so a plausible-looking key can pin
	// nothing at all.
	if counts, cerr := store.CountBySource(ctx, time.Time{}, time.Time{}); cerr == nil {
		for _, key := range cfg.UnmatchedRetentionSources(counts) {
			warnings = append(warnings, fmt.Sprintf(
				"retention.keep_by_source[%q] matches no source that has produced events — "+
					"the rule is doing nothing; check the tag with `tokenops vendor-usage status`", key))
		}
	}
	return warnings
}

// dropWarningFrom renders the telemetry-loss warning carried on /healthz,
// or "" when there is nothing to report.
//
// A daemon predating the field answers without it. That is an unknown, not a
// zero: reporting "0 dropped" for a daemon that never said so would be the
// same false reassurance this warning exists to remove, so a missing or
// non-numeric value stays silent.
func dropWarningFrom(health endpointResult) string {
	raw, ok := health.Body["dropped_events"]
	if !ok {
		return ""
	}
	n, ok := raw.(float64) // encoding/json decodes every number as float64
	if !ok {
		return ""
	}
	return events.DropWarning(int64(n))
}

func writeStatusText(w io.Writer, base string, r statusResult) error {
	insight := r.Insight
	if insight.Level == "" {
		insight = presentation.ForStatus(statusState(r))
	}
	lines := []string{
		fmt.Sprintf("daemon: %s", base),
		fmt.Sprintf("status: %s — %s", insight.Level, insight.Summary),
		formatLine("health ", r.Health),
		formatLine("ready  ", r.Ready),
		formatLine("version", r.Version),
	}
	if len(r.Warnings) > 0 {
		lines = append(lines, "warnings:")
		for _, warn := range r.Warnings {
			lines = append(lines, "  ! "+warn)
		}
	}
	_, err := fmt.Fprintln(w, strings.Join(lines, "\n"))
	return err
}

func formatLine(label string, r endpointResult) string {
	if r.Error != "" {
		return fmt.Sprintf("  %s  ERR  %s", label, r.Error)
	}
	if len(r.Body) == 0 {
		return fmt.Sprintf("  %s  %d", label, r.Status)
	}
	return fmt.Sprintf("  %s  %d  %s", label, r.Status, compactBody(r.Body))
}

func compactBody(body map[string]any) string {
	b, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	return string(b)
}

// offlineRemedy says how to get a stopped daemon running on this machine.
// Where a unit is installed, `tokenops start` would start a second daemon
// beside the one launchd or systemd keeps trying to run, so the remedy is
// the supervisor's restart; without one, installing the unit beats a
// foreground process that dies with its terminal.
func offlineRemedy(supervised bool) string {
	if supervised {
		return "it is supervised but not answering — run `tokenops daemon restart`, and check `tokenops daemon status` if it does not come up"
	}
	return "run `tokenops daemon install` to start it and keep it running (or `tokenops start` in a terminal you keep open)"
}
