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
			if target == "" {
				cfg, err := loadConfig(rf)
				if err != nil {
					return err
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
				return err
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
