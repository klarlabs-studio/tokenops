package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ensureDaemon makes "mode: active" actually do something: the live
// routing middleware and the spend watcher run in the daemon, so
// activating the mode without one is a silent no-op. Returns a
// human-readable status for the tool response.
func (d ModeDeps) ensureDaemon(configPath string) string {
	if url, ok := daemonAlive(); ok {
		return fmt.Sprintf("already running at %s — it loaded its config at boot; restart it (`tokenops start`) to apply active mode", url)
	}
	start := d.StartDaemon
	if start == nil {
		start = startDaemonDetached
	}
	pid, logPath, err := start(configPath)
	if err != nil {
		return "not running and could not be started: " + err.Error() + "; run `tokenops start` manually"
	}
	return fmt.Sprintf("started (pid %d) with active mode; logs: %s", pid, logPath)
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

// probeDaemon reads the URL hint and asks the daemon how it is doing. A
// stale hint (daemon died without cleanup) fails the HTTP probe and reads
// as not-running.
func probeDaemon() DaemonReport {
	hint, err := readURLHint()
	if err != nil || hint == nil || hint.URL == "" {
		return DaemonReport{}
	}
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(hint.URL + "/healthz")
	if err != nil {
		return DaemonReport{}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return DaemonReport{URL: hint.URL}
	}
	out := DaemonReport{URL: hint.URL, Alive: true}
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

// daemonAlive reports whether a daemon is reachable, for the mode tools,
// which care about presence and nothing else.
func daemonAlive() (string, bool) {
	r := probeDaemon()
	return r.URL, r.Alive
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
