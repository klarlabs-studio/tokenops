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
	URL            string    `json:"url"`
	LocalURL       string    `json:"local_url,omitempty"`
	Addr           string    `json:"addr"`
	TLS            bool      `json:"tls"`
	PID            int       `json:"pid"`
	StartedAt      time.Time `json:"started_at"`
	DashboardToken string    `json:"dashboard_token,omitempty"`
}

// preferredURL returns the URL the dashboard tool should hand to the
// agent: mDNS hostname when the daemon successfully advertised, the
// loopback URL otherwise. Keeping the choice in one place means a
// future addition (Tailscale MagicDNS, dynamic DNS) plugs in here
// without touching the tool handler.
func (p urlHintPayload) preferredURL() string {
	if p.LocalURL != "" {
		return p.LocalURL
	}
	return p.URL
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

// DashboardDeps wires tokenops_dashboard.
type DashboardDeps struct {
	// DaemonURL is the configured listen address as a URL (see
	// ConfiguredDaemonURL), asked when the URL hint is missing. Empty
	// skips that probe.
	DaemonURL string
	// UnitInstalled reports whether a launchd/systemd unit supervises
	// the daemon, so the not-running hint names the command that fits
	// this machine. nil gives both options.
	UnitInstalled func() bool
	// Token returns the dashboard auth token when the URL hint, which
	// normally carries it, is missing. nil or "" sends the link without
	// one.
	Token func() string
}

// RegisterDashboardTool mounts tokenops_dashboard. The tool returns a
// markdown link to the daemon's dashboard when the daemon is running;
// otherwise returns a structured error naming the command that brings
// the daemon back (mirrors the disabled-subsystem contract used by
// other tools).
//
// The tool deliberately takes no inputs: the operator doesn't pick
// a URL, they discover the one their daemon is already serving.
func RegisterDashboardTool(s *Server, d DashboardDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_dashboard").
		Description("Return a clickable URL to the local TokenOps dashboard (Vue + D3 charts of cost, tokens, and burn rate served by the daemon). Returns a structured `{error, hint}` payload when the daemon is not running.").
		Handler(func(_ context.Context, _ emptyInput) (string, error) {
			payload, err := readURLHint()
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return dashboardWithoutHint(d), nil
				}
				return jsonString(map[string]string{
					"error": "url_hint_read_failed",
					"hint":  "could not read daemon URL hint: " + err.Error(),
				}), nil
			}
			base := payload.preferredURL()
			dashURL := base + "/dashboard"
			if payload.DashboardToken != "" {
				dashURL += "?token=" + payload.DashboardToken
			}
			summary := "## Dashboard\n\n[Open " + dashURL + "](" + dashURL + ")\n\n"
			if payload.LocalURL != "" && payload.LocalURL != payload.URL {
				summary += "_mDNS: " + payload.LocalURL + " — falls back to " + payload.URL + " if `.local` resolution is off._\n\n"
			}
			if payload.DashboardToken != "" {
				summary += "_The URL carries a one-shot auth token; first click sets a session cookie and the address bar drops the token._\n\n"
			}
			summary += "_Daemon PID " + strconv.Itoa(payload.PID) + ", started " + payload.StartedAt.Format(time.RFC3339) + "._\n"
			return markdownPayload(summary, map[string]any{
				"url":             dashURL,
				"daemon_url":      base,
				"loopback":        payload.URL,
				"local_url":       payload.LocalURL,
				"tls":             payload.TLS,
				"pid":             payload.PID,
				"started_at":      payload.StartedAt,
				"auth_token":      payload.DashboardToken,
				"auth_token_hint": "send as ?token=… query, Authorization: Bearer header, or session cookie",
			}), nil
		})
	return nil
}

// dashboardWithoutHint answers when the URL hint is missing. The hint once
// vanished while the daemon kept serving, and this tool then told the
// operator to start a daemon that was already running. So it asks the
// configured address first, the same fallback status uses.
//
// A daemon found that way still serves the dashboard, but the auth token
// travels in the hint, so the link goes out without it and says how to get
// the hint back.
func dashboardWithoutHint(d DashboardDeps) string {
	r := probeDaemonAt(d.DaemonURL)
	if !r.Alive {
		return jsonString(map[string]string{
			"error": "daemon_not_running",
			"hint":  "the dashboard is served by the ingestion daemon: " + daemonRemedy(d.UnitInstalled) + ", then call this tool again",
		})
	}
	dashURL := r.URL + "/dashboard"
	if d.Token != nil {
		if tok := d.Token(); tok != "" {
			link := dashURL + "?token=" + tok
			summary := "## Dashboard\n\n[Open " + dashURL + "](" + link + ")\n\n" +
				"_The URL carries a one-shot auth token; first click sets a session cookie and the address bar drops the token._\n"
			return markdownPayload(summary, map[string]any{
				"url":             link,
				"daemon_url":      r.URL,
				"loopback":        r.URL,
				"auth_token":      tok,
				"auth_token_hint": "send as ?token=… query, Authorization: Bearer header, or session cookie",
			})
		}
	}
	note := "the daemon's URL hint is missing and no dashboard token could be read, so this link carries no auth token; if the dashboard asks for one, " +
		"restarting the daemon rewrites the hint (`tokenops daemon restart` where a supervisor unit is installed)"
	summary := "## Dashboard\n\n[Open " + dashURL + "](" + dashURL + ")\n\n_" + note + "._\n"
	return markdownPayload(summary, map[string]any{
		"url":        dashURL,
		"daemon_url": r.URL,
		"loopback":   r.URL,
		"note":       note,
	})
}
