package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// daemonFetchTimeout bounds each read from the ingestion daemon. The daemon
// is on loopback and has already answered /healthz by the time these run, so
// anything slower than this is a daemon in trouble, not a slow network.
const daemonFetchTimeout = 2 * time.Second

// DaemonDomainEvents is what the daemon's /api/domain-events answered.
type DaemonDomainEvents struct {
	Counts map[string]int64 `json:"counts"`
	Total  int64            `json:"total"`
	// AuditDropped is absent when the daemon has no audit subscriber.
	AuditDropped *int64 `json:"audit_dropped,omitempty"`
}

// FetchDaemonVersion returns the version the ingestion daemon at baseURL
// reports on /version, or "" when it cannot be read. An unknown version
// stays unknown rather than being guessed.
//
// Kept apart from the /healthz probe: the probe answers "is anything
// ingesting", and a version is only worth a second round trip once it has.
func FetchDaemonVersion(baseURL string) string {
	var body struct {
		Version string `json:"version"`
	}
	if err := getDaemonJSON(baseURL, "/version", &body); err != nil {
		return ""
	}
	return body.Version
}

// FetchDaemonDomainEvents reads the per-kind domain-event counters from the
// ingestion daemon at baseURL. The counters live in the daemon's process —
// the MCP server publishes no domain events of its own — so this is the only
// place real counts can come from.
func FetchDaemonDomainEvents(baseURL string) (DaemonDomainEvents, error) {
	var out DaemonDomainEvents
	if err := getDaemonJSON(baseURL, "/api/domain-events", &out); err != nil {
		return DaemonDomainEvents{}, err
	}
	return out, nil
}

func getDaemonJSON(baseURL, path string, out any) error {
	if baseURL == "" {
		return fmt.Errorf("no daemon URL")
	}
	client := http.Client{Timeout: daemonFetchTimeout}
	resp, err := client.Get(baseURL + path)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(out)
}
