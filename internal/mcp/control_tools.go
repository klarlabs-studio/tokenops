package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/version"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// staleWarnings renders one operator-facing warning per stale vendor-
// usage source. Nil-safe: a nil StaleSources hook (no store wired) or an
// empty result yields no warnings, so status never panics or blocks on a
// missing store.
func staleWarnings(d ControlDeps) []string {
	if d.StaleSources == nil {
		return nil
	}
	stale := d.StaleSources()
	if len(stale) == 0 {
		return nil
	}
	warnings := make([]string, 0, len(stale))
	for _, s := range stale {
		warnings = append(warnings, s.Warning())
	}
	return warnings
}

// ControlDeps wires the in-process state the control tools surface. The
// daemon constructs this once at startup and passes it through alongside
// the analytics deps; CLI talks to the same data via the HTTP control
// endpoints (/healthz, /readyz, /version).
type ControlDeps struct {
	// ConfigJSON is the marshalled active configuration. Built once at
	// daemon start so the tool can return it without re-reading disk.
	ConfigJSON json.RawMessage
	// Config is the parsed configuration. When non-nil, statusInfo
	// derives blockers + next_actions from it so first-run callers can
	// see which subsystems gate populated data.
	Config *config.Config
	// ReadyCheck reports daemon readiness. Returns true once the proxy
	// has finished its boot sequence.
	ReadyCheck func() bool
	// EventCounts, when set, returns per-kind domain-event counters.
	// observ.EventCounter.Counts satisfies this signature.
	EventCounts func() map[string]int64
	// AuditDrops, when set, returns the number of audit-subscriber
	// events shed under backpressure.
	AuditDrops func() int64
	// StaleSources, when set, returns the enabled vendor-usage sources
	// that have ingested no events recently. Nil-safe: nil means the
	// check is unavailable (e.g. no store wired) and status omits
	// warnings entirely. Surfaced as soft `warnings`, never blockers.
	StaleSources func() []config.StaleSource
}

type emptyInput struct{}

// versionResult is the typed payload for tokenops_version. Advertised as
// the tool's outputSchema so clients receive typed structuredContent.
type versionResult struct {
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	Date          string `json:"date"`
	Display       string `json:"display"`
	SchemaVersion string `json:"schema_version"`
}

// statusResult is the typed payload for tokenops_status.
type statusResult struct {
	Status        string   `json:"status"`
	Ready         bool     `json:"ready"`
	State         string   `json:"state"`
	Version       string   `json:"version"`
	SchemaVersion string   `json:"schema_version"`
	Blockers      []string `json:"blockers"`
	NextActions   []string `json:"next_actions"`
	Warnings      []string `json:"warnings,omitempty"`
}

// domainEventsResult is the typed payload for tokenops_domain_events.
type domainEventsResult struct {
	Counts       map[string]int64 `json:"counts"`
	Total        int64            `json:"total"`
	AuditDropped *int64           `json:"audit_dropped,omitempty"`
}

// RegisterControlTools adds version / status / config / domain_events
// tools that mirror the equivalent CLI commands and HTTP control
// endpoints.
func RegisterControlTools(s *Server, d ControlDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_version").
		Description("Return TokenOps daemon build metadata. Mirrors `tokenops version` and the daemon's /version endpoint.").
		OutputSchema(versionResult{}).
		Handler(func(_ context.Context, _ emptyInput) (versionResult, error) {
			return versionInfo(), nil
		})

	s.Tool("tokenops_status").
		Description("Return daemon readiness + version. Mirrors `tokenops status` (which queries /healthz, /readyz, /version over HTTP).").
		OutputSchema(statusResult{}).
		Handler(func(_ context.Context, _ emptyInput) (statusResult, error) {
			return statusInfo(d), nil
		})

	s.Tool("tokenops_config").
		Description("Return the active daemon configuration (redacted). Mirrors `tokenops config show`.").
		Handler(func(_ context.Context, _ emptyInput) (string, error) {
			return configInfo(d), nil
		})

	s.Tool("tokenops_domain_events").
		Description("Return per-kind counts of in-process domain events (workflow.started, optimization.applied, rule_corpus.reloaded, budget.exceeded, ...). Mirrors the audit/observ wiring; safe to poll.").
		OutputSchema(domainEventsResult{}).
		Handler(func(_ context.Context, _ emptyInput) (domainEventsResult, error) {
			return domainEventsInfo(d), nil
		})
	return nil
}

func versionInfo() versionResult {
	return versionResult{
		Version:       version.Version,
		Commit:        version.Commit,
		Date:          version.Date,
		Display:       version.String(),
		SchemaVersion: eventschema.SchemaVersion,
	}
}

func statusInfo(d ControlDeps) statusResult {
	ready := false
	if d.ReadyCheck != nil {
		ready = d.ReadyCheck()
	}
	blockers := []string{}
	if d.Config != nil {
		blockers = d.Config.Blockers()
	}
	nextActions := config.NextActionsFor(blockers)
	state := "not_ready"
	switch {
	case ready && len(blockers) == 0:
		state = "ready"
	case ready && len(blockers) > 0:
		// MCP serve opens its own store and is functionally healthy
		// even when daemon-side subsystems are off. Surface that as
		// `degraded` so callers can distinguish "broken" from
		// "running with reduced surface area".
		state = "degraded"
	case !ready && len(blockers) > 0:
		state = "not_configured"
	}

	// Runtime ingestion staleness is a softer signal than config
	// blockers: an enabled vendor-usage poller that has ingested nothing
	// recently means status is quietly serving stale/$0 data. Surface it
	// as `warnings` (never blockers), add a remediation next_action, and
	// downgrade a `ready` state to `degraded` while keeping ready:true.
	warnings := staleWarnings(d)
	if len(warnings) > 0 {
		nextActions = append(nextActions, config.StaleIngestionNextAction)
		if state == "ready" {
			state = "degraded"
		}
	}

	return statusResult{
		Status:        "ok",
		Ready:         ready,
		State:         state,
		Version:       version.String(),
		SchemaVersion: eventschema.SchemaVersion,
		Blockers:      blockers,
		NextActions:   nextActions,
		Warnings:      warnings,
	}
}

func configInfo(d ControlDeps) string {
	if len(d.ConfigJSON) == 0 {
		return jsonString(map[string]any{"error": "config snapshot not available"})
	}
	return string(d.ConfigJSON)
}

func domainEventsInfo(d ControlDeps) domainEventsResult {
	counts := map[string]int64{}
	if d.EventCounts != nil {
		counts = d.EventCounts()
	}
	var total int64
	for _, v := range counts {
		total += v
	}
	res := domainEventsResult{Counts: counts, Total: total}
	if d.AuditDrops != nil {
		dropped := d.AuditDrops()
		res.AuditDropped = &dropped
	}
	return res
}
