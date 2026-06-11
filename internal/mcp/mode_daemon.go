package mcp

import (
	"fmt"
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

// daemonAlive reports whether a daemon is reachable: the URL hint file
// exists and its /healthz endpoint answers. A stale hint (daemon died
// without cleanup) fails the HTTP probe and reads as not-running.
func daemonAlive() (string, bool) {
	hint, err := readURLHint()
	if err != nil || hint == nil || hint.URL == "" {
		return "", false
	}
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(hint.URL + "/healthz")
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	return hint.URL, resp.StatusCode == http.StatusOK
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
