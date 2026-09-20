package mcp

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/daemonhint"
)

// urlHintPayload is the daemon's URL hint. The reader lives in
// internal/infra/daemonhint because the CLI needs the same file — both
// resolve the running daemon's address, and both need its dashboard token
// to call the credentialed /api/* routes.
type urlHintPayload = daemonhint.Payload

// urlHintPath resolves the location the daemon writes its URL hint to.
func urlHintPath() (string, error) { return daemonhint.Path() }

// readURLHint loads the daemon URL hint. Returns os.ErrNotExist when
// no daemon is running so callers can branch on a typed error.
func readURLHint() (*urlHintPayload, error) { return daemonhint.Read() }

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
			base := payload.PreferredURL()
			dashURL := base + "/dashboard"
			if payload.DashboardToken != "" {
				dashURL += "?token=" + payload.DashboardToken
			}
			summary := "## Dashboard\n\n[Open " + dashURL + "](" + dashURL + ")\n\n"
			if payload.LocalURL != "" && payload.LocalURL != payload.URL {
				summary += "_mDNS: " + payload.LocalURL + " — falls back to " + payload.URL + " if `.local` resolution is off._\n\n"
			}
			if payload.DashboardToken != "" {
				summary += "_The URL carries the dashboard's auth token; the first click exchanges it for a session cookie and the address bar drops it. The token itself stays valid — rotate it with `tokenops dashboard rotate-token`._\n\n"
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
				"_The URL carries the dashboard's auth token; the first click exchanges it for a session cookie and the address bar drops it. The token itself stays valid — rotate it with `tokenops dashboard rotate-token`._\n"
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
