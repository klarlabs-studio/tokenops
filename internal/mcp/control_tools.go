package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/events"
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
	// ConfigJSON is the marshalled active configuration, used only when
	// no ConfigGetter is wired.
	ConfigJSON json.RawMessage
	// Config is the parsed configuration. When non-nil, statusInfo
	// derives blockers + next_actions from it so first-run callers can
	// see which subsystems gate populated data. ConfigGetter, when set,
	// takes precedence.
	Config *config.Config
	// ConfigGetter returns the live configuration at call time.
	//
	// serve outlives config writes: tokenops_plan_set and
	// tokenops_vendor_usage_setup rewrite config.yaml under a running
	// server, and with only the startup snapshot tokenops_config and the
	// status blockers kept reporting the pre-write state until the MCP
	// client restarted.
	ConfigGetter func() *config.Config
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
	// DaemonProbe, when set, asks the ingestion daemon how it is doing:
	// whether it is reachable, and how much telemetry it has failed to
	// persist. Nil means the check is unavailable and status stays
	// silent — an unknown is not evidence of absence.
	//
	// One probe carries both because they are one /healthz response, and
	// because the two answers belong together: a daemon that is running
	// and losing rows looks healthy from every other angle.
	DaemonProbe func() DaemonReport
	// DaemonVersion, when set, returns the version the daemon at the given
	// URL reports, or "" when unknown. Only asked of a daemon the probe
	// found alive.
	DaemonVersion func(url string) string
	// DaemonDomainEvents, when set, reads the domain-event counters from
	// the daemon at the given URL. The counters live in the daemon's
	// process, so without this the MCP server has no counts to report.
	DaemonDomainEvents func(url string) (DaemonDomainEvents, error)
	// BinaryDrift, when set, reports whether the binary this MCP server
	// runs has been replaced by a newer install. Nil means unchecked.
	BinaryDrift func() BinaryDrift
}

// activeConfig prefers the live getter and falls back to the static
// snapshot for callers that wire no watcher.
func (d ControlDeps) activeConfig() *config.Config {
	if d.ConfigGetter != nil {
		return d.ConfigGetter()
	}
	return d.Config
}

type emptyInput struct{}

// versionResult is the typed payload for tokenops_version. Advertised as
// the tool's outputSchema so clients receive typed structuredContent.
type versionResult struct {
	Version       string `json:"version" jsonschema:"description=version of this MCP server process"`
	Commit        string `json:"commit"`
	Date          string `json:"date"`
	Display       string `json:"display"`
	SchemaVersion string `json:"schema_version"`
	// DaemonVersion is the ingestion daemon's version, when one answers.
	// The two are separate processes and upgrade separately.
	DaemonVersion   string   `json:"daemon_version,omitempty" jsonschema:"description=version of the ingestion daemon when one is reachable"`
	ServerOutOfDate bool     `json:"server_out_of_date,omitempty" jsonschema:"description=true when a newer tokenops has been installed since this MCP server started"`
	Warnings        []string `json:"warnings,omitempty"`
	NextActions     []string `json:"next_actions,omitempty"`
}

// statusResult is the typed payload for tokenops_status.
type statusResult struct {
	Status        string   `json:"status"`
	Ready         bool     `json:"ready"`
	State         string   `json:"state"`
	Version       string   `json:"version" jsonschema:"description=version of this MCP server process"`
	SchemaVersion string   `json:"schema_version"`
	Blockers      []string `json:"blockers"`
	NextActions   []string `json:"next_actions"`
	Warnings      []string `json:"warnings,omitempty"`
	// DaemonVersion and ServerOutOfDate mirror tokenops_version, so the one
	// call an agent makes first says whether it is talking to old code.
	DaemonVersion   string `json:"daemon_version,omitempty" jsonschema:"description=version of the ingestion daemon when one is reachable"`
	ServerOutOfDate bool   `json:"server_out_of_date,omitempty" jsonschema:"description=true when a newer tokenops has been installed since this MCP server started"`
}

// domainEventsResult is the typed payload for tokenops_domain_events.
//
// Counts and Total are omitted rather than zero when the counts cannot be
// read: the payload then carries error + hint and no numbers, so
// "unavailable" can never be mistaken for "nothing happened".
type domainEventsResult struct {
	Counts       map[string]int64 `json:"counts,omitempty"`
	Total        *int64           `json:"total,omitempty"`
	AuditDropped *int64           `json:"audit_dropped,omitempty"`
	// Spans says when each kind's counted events happened. The daemon
	// replays its persisted log at boot, so counts are lifetime totals:
	// read last_at before treating a count as current.
	Spans  map[string]EventSpan `json:"spans,omitempty"`
	Source string               `json:"source,omitempty" jsonschema:"description=where the counts came from: daemon or in_process"`
	Error  string               `json:"error,omitempty"`
	Hint   string               `json:"hint,omitempty"`
}

// RegisterControlTools adds version / status / config / domain_events
// tools that mirror the equivalent CLI commands and HTTP control
// endpoints.
func RegisterControlTools(s *Server, d ControlDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_version").
		Description("Return this MCP server's build metadata, the ingestion daemon's version when one is reachable, and whether a newer tokenops has been installed since this server started. The MCP server is a child of the client and keeps running old code after an upgrade until the client restarts it; server_out_of_date says so. Mirrors `tokenops version` and the daemon's /version endpoint.").
		OutputSchema(versionResult{}).
		Handler(func(_ context.Context, _ emptyInput) (versionResult, error) {
			return versionInfo(d), nil
		})

	s.Tool("tokenops_status").
		Description("Return readiness, this MCP server's version and the ingestion daemon's, config blockers, and warnings — including when this MCP server is out of date against the installed tokenops. Mirrors `tokenops status` (which queries /healthz, /readyz, /version over HTTP).").
		OutputSchema(statusResult{}).
		Handler(func(_ context.Context, _ emptyInput) (statusResult, error) {
			return statusInfo(d), nil
		})

	s.Tool("tokenops_config").
		Description("Return the active configuration (redacted), read at call time so changes written by other tools are reflected. Mirrors `tokenops config show`.").
		Handler(func(_ context.Context, _ emptyInput) (string, error) {
			return configInfo(d), nil
		})

	s.Tool("tokenops_domain_events").
		Description("Return per-kind domain-event counts (workflow.started, optimization.applied, rule_corpus.reloaded, budget.exceeded, ...) as counted by the ingestion daemon since it started, including events it replayed from its domain-event log at boot. Read from the daemon's /api/domain-events; when no daemon is reachable returns error=unavailable_in_mcp_server with a hint instead of counts. Mirrors `tokenops events`; safe to poll. Counts include events replayed from the daemon's persisted log, so they are lifetime totals: spans gives each kind's first_at and last_at; check last_at before treating a count as current.").
		OutputSchema(domainEventsResult{}).
		Handler(func(_ context.Context, _ emptyInput) (domainEventsResult, error) {
			return domainEventsInfo(d), nil
		})
	return nil
}

func versionInfo(d ControlDeps) versionResult {
	res := versionResult{
		Version:       version.Version,
		Commit:        version.Commit,
		Date:          version.Date,
		Display:       version.String(),
		SchemaVersion: eventschema.SchemaVersion,
	}
	if d.DaemonProbe != nil {
		res.DaemonVersion = daemonVersion(d, d.DaemonProbe())
	}
	if drift := binaryDrift(d); drift.OutOfDate {
		res.ServerOutOfDate = true
		res.Warnings = []string{drift.Warning()}
		res.NextActions = []string{StaleServerNextAction}
	}
	return res
}

// daemonVersion asks a live daemon for its version. A daemon the probe did
// not reach is not asked: that would add a timeout to every call to learn
// nothing.
func daemonVersion(d ControlDeps, r DaemonReport) string {
	if d.DaemonVersion == nil || !r.Alive || r.URL == "" {
		return ""
	}
	return d.DaemonVersion(r.URL)
}

func statusInfo(d ControlDeps) statusResult {
	ready := false
	if d.ReadyCheck != nil {
		ready = d.ReadyCheck()
	}
	blockers := []string{}
	if cfg := d.activeConfig(); cfg != nil {
		blockers = cfg.Blockers()
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

	// One probe answers both questions below.
	var report DaemonReport
	probed := d.DaemonProbe != nil
	if probed {
		report = d.DaemonProbe()
	}

	// Telemetry the daemon could not write. Reported even while it is
	// alive and ready, because that is exactly the case that looks
	// healthy from every other angle: the daemon is up, the store opens,
	// queries answer — against totals that are quietly short.
	if w := events.DropWarning(report.Dropped); w != "" {
		warnings = append(warnings, w)
		nextActions = append(nextActions, events.DropNextAction)
		if state == "ready" {
			state = "degraded"
		}
	}

	// A missing ingestion daemon is the cause; stale sources are the
	// symptom. It is reported first because it is unambiguous — a quiet
	// source can mean "not used lately", an absent daemon cannot — and
	// because it fires immediately rather than after the stale window.
	//
	// Not a blocker: serve genuinely answers queries against the store it
	// has. It degrades `ready` the same way stale ingestion does, so the
	// distinction stays "running with reduced surface area", not "broken".
	if w := daemonPresenceWarning(report, probed); w != "" {
		warnings = append([]string{w}, warnings...)
		nextActions = append(nextActions, DaemonPresenceNextAction)
		if state == "ready" {
			state = "degraded"
		}
	}

	if len(warnings) > 0 {
		nextActions = append(nextActions, config.StaleIngestionNextAction)
		if state == "ready" {
			state = "degraded"
		}
	}

	// Checked after the ingestion block so the vendor-usage remediation is
	// not attached to it: a stale server has nothing to do with a silent
	// source. Listed first because it qualifies everything else — every
	// other answer here comes from the old code.
	drift := binaryDrift(d)
	if drift.OutOfDate {
		warnings = append([]string{drift.Warning()}, warnings...)
		nextActions = append(nextActions, StaleServerNextAction)
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

		DaemonVersion:   daemonVersion(d, report),
		ServerOutOfDate: drift.OutOfDate,
	}
}

// configInfo marshals the live config through Snapshot, which redacts every
// secret (session key, admin key, cookies, tokens). The raw Config must never
// reach this output.
func configInfo(d ControlDeps) string {
	if d.ConfigGetter != nil {
		if cfg := d.ConfigGetter(); cfg != nil {
			if data, err := cfg.Snapshot(); err == nil {
				return string(data)
			}
		}
		return jsonString(map[string]any{"error": "config snapshot not available"})
	}
	if len(d.ConfigJSON) == 0 {
		return jsonString(map[string]any{"error": "config snapshot not available"})
	}
	return string(d.ConfigJSON)
}

// domainEventsUnavailable is the error code for counts this process cannot
// see. Domain events are published and counted inside the ingestion daemon;
// `tokenops serve` is a different process with no bus of its own.
const domainEventsUnavailable = "unavailable_in_mcp_server"

// domainEventsInfo reports domain-event counts from wherever they are
// actually counted: in-process when this process runs the bus, otherwise the
// daemon over HTTP.
//
// serve used to answer {"counts":{},"total":0} because it never had the
// counters wired — an empty map rendered as "nothing happened" for a fact
// it could not see. When no source is reachable it now says so.
func domainEventsInfo(d ControlDeps) domainEventsResult {
	if d.EventCounts != nil {
		counts := d.EventCounts()
		var total int64
		for _, v := range counts {
			total += v
		}
		res := domainEventsResult{Counts: counts, Total: &total, Source: "in_process"}
		if d.AuditDrops != nil {
			dropped := d.AuditDrops()
			res.AuditDropped = &dropped
		}
		return res
	}
	if d.DaemonProbe == nil || d.DaemonDomainEvents == nil {
		return domainEventsResult{
			Error: domainEventsUnavailable,
			Hint:  "domain events are counted inside the ingestion daemon ('tokenops start'), not this MCP server; run 'tokenops events' to read them",
		}
	}
	report := d.DaemonProbe()
	if !report.Alive {
		return domainEventsResult{
			Error: domainEventsUnavailable,
			Hint:  "domain events are counted inside the ingestion daemon and none is reachable; get it running — " + config.DaemonRunRemedy + " ('tokenops events' can tally the persisted domain-event log meanwhile)",
		}
	}
	ev, err := d.DaemonDomainEvents(report.URL)
	if err != nil {
		return domainEventsResult{
			Error: domainEventsUnavailable,
			Hint:  fmt.Sprintf("the ingestion daemon at %s did not return its domain-event counts (%v); run 'tokenops events' to read them", report.URL, err),
		}
	}
	total := ev.Total
	return domainEventsResult{
		Counts:       ev.Counts,
		Total:        &total,
		AuditDropped: ev.AuditDropped,
		Spans:        ev.Spans,
		Source:       "daemon",
	}
}
