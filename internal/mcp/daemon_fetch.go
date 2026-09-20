package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/infra/daemonhint"
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
	// Spans says when each kind's counted events happened; absent from a
	// daemon older than this field.
	Spans map[string]EventSpan `json:"spans,omitempty"`
}

// EventSpan is when the counted events of one kind happened.
type EventSpan struct {
	First time.Time `json:"first_at"`
	Last  time.Time `json:"last_at"`
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
	req, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
	if err != nil {
		return err
	}
	// /api/* is credentialed. The token lives in the daemon's URL hint,
	// which is 0600 and readable by whoever started the daemon — that is
	// this process. A missing token sends the request bare and lets the
	// daemon answer 401, which is a better diagnosis than refusing to ask.
	if tok := daemonhint.Token(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := http.Client{Timeout: daemonFetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(out)
}

// DaemonSources is what the daemon's /api/sources answered: one report
// per configured ingestion source, healthy ones included.
type DaemonSources struct {
	Sources   []freshness.Report `json:"sources"`
	Unhealthy int                `json:"unhealthy"`
}

// errDaemonUnreachable marks a freshness read that failed because no
// daemon answered, as opposed to one that answered with nothing.
var errDaemonUnreachable = errors.New("mcp: ingestion daemon did not answer /api/sources")

// FetchDaemonSources reads per-source ingestion health from the daemon.
//
// It has to come from there. The daemon is the only process that holds
// all three facts at once — what was ingested, what each origin holds,
// and whether each reader is still succeeding — because it owns the
// store and constructs the pollers. `tokenops serve` is a different
// process and can see none of the last two.
//
// A daemon too old to serve the route answers 404, which surfaces as an
// error and leaves the caller with counts alone: the behaviour before
// this existed.
func FetchDaemonSources(baseURL string) (DaemonSources, error) {
	var out DaemonSources
	if err := getDaemonJSON(baseURL, "/api/sources", &out); err != nil {
		return DaemonSources{}, fmt.Errorf("%w: %w", errDaemonUnreachable, err)
	}
	return out, nil
}
