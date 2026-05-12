package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/felixgeelhaar/tokenops/internal/config"
	"github.com/felixgeelhaar/tokenops/internal/version"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

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
}

type emptyInput struct{}

// RegisterControlTools adds version / status / config / domain_events
// tools that mirror the equivalent CLI commands and HTTP control
// endpoints.
func RegisterControlTools(s *Server, d ControlDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_version").
		Description("Return TokenOps daemon build metadata. Mirrors `tokenops version` and the daemon's /version endpoint.").
		Handler(func(_ context.Context, _ emptyInput) (string, error) {
			return versionInfo(), nil
		})

	s.Tool("tokenops_status").
		Description("Return daemon readiness + version. Mirrors `tokenops status` (which queries /healthz, /readyz, /version over HTTP).").
		Handler(func(_ context.Context, _ emptyInput) (string, error) {
			return statusInfo(d), nil
		})

	s.Tool("tokenops_config").
		Description("Return the active daemon configuration (redacted). Mirrors `tokenops config show`.").
		Handler(func(_ context.Context, _ emptyInput) (string, error) {
			return configInfo(d), nil
		})

	s.Tool("tokenops_domain_events").
		Description("Return per-kind counts of in-process domain events (workflow.started, optimization.applied, rule_corpus.reloaded, budget.exceeded, ...). Mirrors the audit/observ wiring; safe to poll.").
		Handler(func(_ context.Context, _ emptyInput) (string, error) {
			return domainEventsInfo(d), nil
		})
	return nil
}

func versionInfo() string {
	return jsonString(map[string]any{
		"version":        version.Version,
		"commit":         version.Commit,
		"date":           version.Date,
		"display":        version.String(),
		"schema_version": eventschema.SchemaVersion,
	})
}

func statusInfo(d ControlDeps) string {
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
	payload := map[string]any{
		"status":         "ok",
		"ready":          ready,
		"state":          state,
		"version":        version.String(),
		"schema_version": eventschema.SchemaVersion,
		"blockers":       blockers,
		"next_actions":   nextActions,
	}
	return jsonString(payload)
}

func configInfo(d ControlDeps) string {
	if len(d.ConfigJSON) == 0 {
		return jsonString(map[string]any{"error": "config snapshot not available"})
	}
	return string(d.ConfigJSON)
}

func domainEventsInfo(d ControlDeps) string {
	counts := map[string]int64{}
	if d.EventCounts != nil {
		counts = d.EventCounts()
	}
	var total int64
	for _, v := range counts {
		total += v
	}
	payload := map[string]any{
		"counts": counts,
		"total":  total,
	}
	if d.AuditDrops != nil {
		payload["audit_dropped"] = d.AuditDrops()
	}
	return jsonString(payload)
}
