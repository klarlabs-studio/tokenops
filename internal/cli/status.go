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

	"github.com/felixgeelhaar/tokenops/internal/config"
	"github.com/felixgeelhaar/tokenops/internal/version"
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
				// instead of an opaque "connection refused".
				return writeOfflineStatus(cmd.OutOrStdout(), base, cfg, cfgErr, jsonOut)
			}
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
	Health  endpointResult `json:"health"`
	Ready   endpointResult `json:"ready"`
	Version endpointResult `json:"version"`
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
		"hint":    "daemon not running; run `tokenops start` to launch it. MCP-only deployments can ignore this and call `tokenops_status` via the MCP host instead.",
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
	if cfgErr == nil {
		blockers := cfg.Blockers()
		if len(blockers) == 0 {
			fmt.Fprintln(w, "  blockers: none — start the daemon with `tokenops start`")
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
	fmt.Fprintln(w, "  hint: run `tokenops start` to launch the daemon, or query `tokenops_status` via your MCP host for the serve-side view.")
	return nil
}

func writeStatusText(w io.Writer, base string, r statusResult) error {
	lines := []string{
		fmt.Sprintf("daemon: %s", base),
		formatLine("health ", r.Health),
		formatLine("ready  ", r.Ready),
		formatLine("version", r.Version),
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
