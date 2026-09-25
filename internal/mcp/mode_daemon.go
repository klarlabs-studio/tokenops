package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/daemonhint"
)

// ensureDaemon makes "mode: active" actually do something: the live
// routing middleware and the spend watcher run in the daemon, so
// activating the mode without one is a silent no-op. Returns a
// human-readable status for the tool response.
//
// A daemon that exists read its config at boot, so the change reaches it
// through ApplyConfig — the supervised restart every other config tool
// uses. This used to answer "restart it (`tokenops start`)", a manual step
// that on a supervised machine started a second daemon, and to spawn a
// detached `tokenops start` whenever the URL hint was missing, even beside
// a healthy launchd/systemd daemon. Under a supervisor the unit restart
// also brings a stopped daemon back, so nothing is ever spawned there. The
// spawn is left for the one case nothing else covers: no unit, and nothing
// answering at the hint or the configured address.
func (d ModeDeps) ensureDaemon(configPath string) string {
	if d.UnitInstalled != nil && d.UnitInstalled() {
		return applyConfig(d.ApplyConfig)
	}
	if daemonAliveAt(d.DaemonURL) {
		return applyConfig(d.ApplyConfig)
	}
	start := d.StartDaemon
	if start == nil {
		start = startDaemonDetached
	}
	pid, logPath, err := start(configPath)
	if err != nil {
		return "not running and could not be started: " + err.Error() +
			"; run `tokenops daemon install` to start it supervised"
	}
	return fmt.Sprintf("started (pid %d) with active mode; logs: %s", pid, logPath)
}

// fallbackProbeTimeout bounds the probe of the configured address. It is a
// loopback round trip; a daemon that takes longer than this to answer
// /healthz is not one a status call should wait on.
const fallbackProbeTimeout = time.Second

// ConfiguredDaemonURL is where a daemon started with cfg listens, as a
// client on this machine reaches it: a wildcard bind becomes loopback, the
// same normalisation the daemon applies when it writes its URL hint. Empty
// when cfg has no usable listen address.
func ConfiguredDaemonURL(cfg config.Config) string {
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil || port == "" {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	if cfg.TLS.Enabled {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

// DaemonReport is what a probe of the ingestion daemon answered.
//
// Presence and telemetry loss arrive together because they come from the
// same /healthz response. Splitting them into two accessors would mean two
// HTTP round trips per status call to read one payload twice.
type DaemonReport struct {
	// URL is where the daemon answered; empty when none did.
	URL string
	// Alive reports whether /healthz returned 200.
	Alive bool
	// Dropped is the rows the daemon failed to persist since it started.
	// Absent from an older daemon's payload, which reads as zero — an
	// unknown that stays silent rather than inventing an alarm.
	Dropped int64
}

// probeDaemonAt asks the daemon how it is doing: first where its URL hint
// says it is, then at fallbackURL — the configured listen address.
//
// The hint alone is not enough. It vanished on the operator's machine while
// the daemon kept running, and status then reported "no ingestion daemon
// is reachable" for a healthy one. A missing or stale hint is a reason to
// ask the configured address, not evidence of absence. An empty
// fallbackURL skips that second probe.
func probeDaemonAt(fallbackURL string) DaemonReport {
	var out DaemonReport
	hintURL := ""
	if hint, err := daemonhint.Read(); err == nil && hint != nil {
		hintURL = hint.URL
	}
	if hintURL != "" {
		if out = probeHealthz(hintURL, 2*time.Second); out.Alive {
			return out
		}
	}
	if fallbackURL != "" && fallbackURL != hintURL {
		if r := probeHealthz(fallbackURL, fallbackProbeTimeout); r.Alive {
			return r
		}
	}
	return out
}

// probeHealthz asks one address for /healthz.
func probeHealthz(url string, timeout time.Duration) DaemonReport {
	client := http.Client{Timeout: timeout}
	resp, err := client.Get(url + "/healthz")
	if err != nil {
		return DaemonReport{}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return DaemonReport{URL: url}
	}
	out := DaemonReport{URL: url, Alive: true}
	var body struct {
		DroppedEvents int64 `json:"dropped_events"`
	}
	// A body we cannot parse still proves the daemon answered. Liveness is
	// the load-bearing half here; the drop count is extra.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&body); err == nil {
		out.Dropped = body.DroppedEvents
	}
	return out
}

// daemonAliveAt reports whether a daemon is reachable, for the mode tools,
// which care about presence and nothing else.
func daemonAliveAt(fallbackURL string) bool {
	return probeDaemonAt(fallbackURL).Alive
}

// startDaemonDetached spawns `tokenops start` as a session leader so it
// survives the MCP serve process. Output goes to daemon.log next to the
// events store.
func startDaemonDetached(configPath string) (int, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, "", fmt.Errorf("resolve executable: %w", err)
	}
	logPath, err := daemonLogPath()
	if err != nil {
		return 0, "", err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return 0, "", err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = logFile.Close() }()

	args := []string{"start"}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}
	return spawnDetached(exe, args, logFile)
}

// daemonLogPath mirrors the data-dir convention the URL hint uses so
// the log lands next to events.db.
func daemonLogPath() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "tokenops", "daemon.log"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "daemon.log"), nil
}
