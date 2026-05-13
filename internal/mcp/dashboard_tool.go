package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// urlHintPayload mirrors the daemon's internal/daemon.urlHintPayload
// struct. Duplicated here (instead of importing) so the mcp package
// doesn't take a dependency on the daemon package — keeps the layer
// boundary clean and avoids import cycles when the daemon eventually
// depends on mcp tooling for the in-process MCP listener.
type urlHintPayload struct {
	URL       string    `json:"url"`
	Addr      string    `json:"addr"`
	TLS       bool      `json:"tls"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// urlHintPath resolves the location the daemon writes its URL hint
// to: $XDG_DATA_HOME/tokenops/daemon.url, fallback ~/.tokenops/daemon.url.
// Keeping the resolver here (rather than re-exporting from daemon)
// keeps the MCP package self-contained.
func urlHintPath() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "tokenops", "daemon.url"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "daemon.url"), nil
}

// readURLHint loads the daemon URL hint. Returns os.ErrNotExist when
// no daemon is running so callers can branch on a typed error.
func readURLHint() (*urlHintPayload, error) {
	p, err := urlHintPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var payload urlHintPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// RegisterDashboardTool mounts tokenops_dashboard. The tool returns a
// markdown link to the daemon's dashboard when the daemon is running;
// otherwise returns a structured error directing the operator to
// `tokenops up` (mirrors the disabled-subsystem contract used by
// other tools).
//
// The tool deliberately takes no inputs: the operator doesn't pick
// a URL, they discover the one their daemon is already serving.
func RegisterDashboardTool(s *Server) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_dashboard").
		Description("Return a clickable URL to the local TokenOps dashboard (Vue + D3 charts of cost, tokens, and burn rate served by the daemon). Returns a structured `{error, hint}` payload when the daemon is not running.").
		Handler(func(_ context.Context, _ emptyInput) (string, error) {
			payload, err := readURLHint()
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return jsonString(map[string]string{
						"error": "daemon_not_running",
						"hint":  "run `tokenops up` in another terminal so the dashboard listener starts, then call this tool again",
					}), nil
				}
				return jsonString(map[string]string{
					"error": "url_hint_read_failed",
					"hint":  "could not read daemon URL hint: " + err.Error(),
				}), nil
			}
			summary := "## Dashboard\n\n[Open " + payload.URL + "/dashboard](" + payload.URL + "/dashboard)\n\n" +
				"_Daemon PID " + strconv.Itoa(payload.PID) + ", started " + payload.StartedAt.Format(time.RFC3339) + "._\n"
			return markdownPayload(summary, map[string]any{
				"url":        payload.URL + "/dashboard",
				"daemon_url": payload.URL,
				"tls":        payload.TLS,
				"pid":        payload.PID,
				"started_at": payload.StartedAt,
			}), nil
		})
	return nil
}
