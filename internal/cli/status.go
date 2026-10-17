package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/state"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/daemon"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/daemonreach"
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
		Short: "Whether TokenOps is working: the daemon, its readers, what to fix",
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
				// Daemon unreachable from here. Fall back to the same
				// self-report the MCP tokenops_status tool emits — config +
				// blockers + next_actions — with why the probe failed, so
				// a sandboxed client is not told to restart a healthy
				// daemon. The offline path can't open the store, so it
				// omits warnings.
				return writeOfflineStatus(cmd.OutOrStdout(), base, cfg, cfgErr, jsonOut, classifyProbe(res))
			}
			res.Reachability = &reachability{Reachable: true, Class: daemonreach.Reachable}
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

// daemonProbe is `tokenops status --json`: what each of the daemon's
// probe endpoints answered, graded. It is not the MCP tool's payload,
// which describes the MCP server's own process.
type daemonProbe struct {
	Health   endpointResult             `json:"health"`
	Ready    endpointResult             `json:"ready"`
	Version  endpointResult             `json:"version"`
	Insight  presentation.StatusInsight `json:"insight"`
	Warnings []string                   `json:"warnings,omitempty"`
	// Reachability is whether this client got an answer, kept apart from
	// what the answer said about the daemon.
	Reachability *reachability `json:"reachability,omitempty"`
}

// reachability is whether this client could reach the daemon and, when it
// could not, why. It is evidence about the client's vantage, not about the
// daemon: a sandboxed agent denied the loopback dial sees a healthy daemon
// as unreachable.
type reachability struct {
	Reachable bool              `json:"reachable"`
	Class     daemonreach.Class `json:"class"`
	// Detail is the failing call and errno, without the request URL.
	Detail  string `json:"detail,omitempty"`
	Summary string `json:"summary,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}

// supervisorEvidence is what this machine's supervisor setup says, read
// without asking the supervisor anything: whether a unit file is installed.
type supervisorEvidence struct {
	Kind          daemon.UnitKind `json:"kind"`
	UnitInstalled bool            `json:"unit_installed"`
	// Evidence labels the source, so a reader does not mistake an
	// installed unit for a running job.
	Evidence string `json:"evidence"`
}

// statusState grades the daemon the way every surface does: its explicit
// /readyz status, degraded by local ingestion or drop warnings.
func statusState(r daemonProbe) string {
	readyz, _ := r.Ready.Body["status"].(string)
	return state.OfDaemon(readyz, len(r.Warnings) > 0)
}

type endpointResult struct {
	URL    string         `json:"url"`
	Status int            `json:"status"`
	Body   map[string]any `json:"body,omitempty"`
	Error  string         `json:"error,omitempty"`
	// err is the transport error behind Error, kept for classification.
	err error
}

func fetchStatus(ctx context.Context, base string) (daemonProbe, error) {
	res := daemonProbe{
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
		out.err = err
		return out
	}
	resp, err := statusClient.Do(req)
	if err != nil {
		out.Error = err.Error()
		out.err = err
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

// classifyProbe says why none of the endpoints answered, from /healthz's
// failure: all three share one address, so they fail the same way.
func classifyProbe(res daemonProbe) reachability {
	err := res.Health.err
	if err == nil && res.Health.Error != "" {
		err = errors.New(res.Health.Error)
	}
	class := daemonreach.Classify(err)
	if class == daemonreach.Reachable {
		class = daemonreach.Unknown
	}
	return reachability{Class: class, Detail: daemonreach.Detail(err), Summary: class.Summary()}
}

// readSupervisorEvidence reports the unit file, or nil on a platform with
// no supervisor we know. The job's state is not queried: that would mean
// running launchctl or systemctl from a status read.
func readSupervisorEvidence(supervised bool) *supervisorEvidence {
	kind, err := daemon.DetectUnitKind()
	if err != nil {
		return nil
	}
	return &supervisorEvidence{
		Kind:          kind,
		UnitInstalled: supervised,
		Evidence:      "unit file on disk; the supervisor's job state was not queried",
	}
}

func (s *supervisorEvidence) String() string {
	if s == nil {
		return "no supervisor detected on this platform"
	}
	if s.UnitInstalled {
		return fmt.Sprintf("a %s unit is installed (unit file; job state not queried)", s.Kind)
	}
	return fmt.Sprintf("no %s unit is installed (unit file)", s.Kind)
}

// writeOfflineStatus renders the same shape the MCP tokenops_status tool
// emits when this client reaches no daemon, with why. Only a refused
// connection is reported as "not running"; every other failure leaves the
// daemon's state unknown from here, because the client's own access — a
// sandbox denying loopback, a wrong address — may be what failed.
func writeOfflineStatus(w io.Writer, base string, cfg config.Config, cfgErr error, jsonOut bool, reach reachability) error {
	supervised := daemonSupervised()
	down := reach.Class.ImpliesDown()
	stateName, health := "unknown", "unknown"
	remedy := reach.Class.Remedy(restartCommand(supervised))
	hint := reach.Summary + "; " + remedy
	if down {
		stateName, health = "not_running", "likely_not_running"
		remedy = offlineRemedy(supervised)
		hint = "daemon not running; " + remedy
	}
	reach.Remedy = remedy
	insight := presentation.ForStatus(stateName)
	sup := readSupervisorEvidence(supervised)
	payload := map[string]any{
		"status":        "daemon_unreachable",
		"daemon":        base,
		"ready":         false,
		"state":         stateName,
		"insight":       insight,
		"hint":          hint + ". MCP-only deployments can ignore this and call `tokenops_status` via the MCP host instead.",
		"version":       version.String(),
		"reachability":  reach,
		"daemon_health": health,
	}
	if sup != nil {
		payload["supervisor"] = sup
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
	writeOfflineText(w, base, insight, reach, sup, cfg, cfgErr)
	return nil
}

func writeOfflineText(w io.Writer, base string, insight presentation.StatusInsight, reach reachability,
	sup *supervisorEvidence, cfg config.Config, cfgErr error) {
	down := reach.Class.ImpliesDown()
	if down {
		fmt.Fprintf(w, "daemon: %s (not running)\n", base)
	} else {
		fmt.Fprintf(w, "daemon: %s (not reachable from this client: %s)\n", base, reach.Class)
	}
	fmt.Fprintf(w, "status: %s — %s\n", insight.Level, insight.Summary)
	fmt.Fprintf(w, "  reachability: %s — %s", reach.Class, reach.Summary)
	if reach.Detail != "" {
		fmt.Fprintf(w, " (%s)", reach.Detail)
	}
	fmt.Fprintln(w)
	health := "unknown from this client"
	if down {
		health = "likely not running"
	}
	fmt.Fprintf(w, "  daemon health: %s; %s\n", health, sup)
	if cfgErr == nil {
		blockers := cfg.Blockers()
		if len(blockers) == 0 {
			fmt.Fprintln(w, "  blockers: none — "+reach.Remedy)
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
	if down {
		fmt.Fprintln(w, "  hint: "+reach.Remedy+", or query `tokenops_status` via your MCP host for the serve-side view.")
		return
	}
	fmt.Fprintln(w, "  hint: "+reach.Remedy+". For the serve-side view, query `tokenops_status` via your MCP host.")
}

// statusStaleWarnings is the store's ingestion warnings, read directly
// like `vendor-usage status`. Best-effort by design: an unresolvable DB
// path or an unopenable store degrades to "no warnings" so the status
// command never fails on the health check.
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
	return state.LocalWarnings(ctx, cfg, store, time.Now())
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

func writeStatusText(w io.Writer, base string, r daemonProbe) error {
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

// restartCommand is the bare command that restarts or starts the daemon on
// this machine, for remedies where a restart is the fallback rather than
// the first step.
func restartCommand(supervised bool) string {
	if supervised {
		return "run `tokenops daemon restart`"
	}
	return "run `tokenops daemon install` (or `tokenops start` in a terminal you keep open)"
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
