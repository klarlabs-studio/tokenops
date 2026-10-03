// Package state is the control plane's own read model: whether TokenOps is
// ready, which mode it acts in, and which of its sources are working. The
// MCP tools and the daemon API both answer from here (ADR 0010), so an
// agent and a menu bar cannot disagree about whether TokenOps is healthy.
package state

import (
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/presentation"
)

// State values, from best to worst.
const (
	Ready         = "ready"
	Degraded      = "degraded"
	NotConfigured = "not_configured"
	NotReady      = "not_ready"
)

// Problem is a warning with the action that resolves it.
type Problem struct {
	Warning    string
	NextAction string
}

// StatusInputs is what the caller observed. Each surface knows different
// things about the process it runs in: the MCP server probes the daemon
// and its own binary, the daemon is the daemon.
type StatusInputs struct {
	// Ready is the process's own readiness.
	Ready bool
	// Config supplies the blockers; nil means none are known.
	Config *config.Config
	// Stale is one warning per enabled source that has gone quiet.
	Stale []string
	// Dropped is how many events the store failed to persist.
	Dropped int64
	// DaemonMissing, when set, says no ingestion daemon answered.
	DaemonMissing *Problem
	// Drift, when set, says this process runs a binary since replaced.
	Drift *Problem
}

// Status is the answer.
type Status struct {
	State       string                     `json:"state"`
	Ready       bool                       `json:"ready"`
	Insight     presentation.StatusInsight `json:"insight"`
	Blockers    []string                   `json:"blockers"`
	NextActions []string                   `json:"next_actions"`
	Warnings    []string                   `json:"warnings,omitempty"`
}

// ComputeStatus grades the inputs. Config blockers decide not_configured;
// everything softer (a quiet source, lost writes, a missing daemon, an
// outdated binary) keeps a ready process ready but degraded, so callers can
// tell "broken" from "running with reduced surface area".
func ComputeStatus(in StatusInputs) Status {
	blockers := []string{}
	if in.Config != nil {
		blockers = in.Config.Blockers()
	}
	out := Status{Ready: in.Ready, Blockers: blockers, NextActions: config.NextActionsFor(blockers)}
	switch {
	case in.Ready && len(blockers) == 0:
		out.State = Ready
	case in.Ready:
		out.State = Degraded
	case len(blockers) > 0:
		out.State = NotConfigured
	default:
		out.State = NotReady
	}
	degrade := func() {
		if out.State == Ready {
			out.State = Degraded
		}
	}

	warnings := append([]string(nil), in.Stale...)
	// Lost writes are reported even while everything else looks healthy:
	// that is exactly when they go unnoticed.
	if w := events.DropWarning(in.Dropped); w != "" {
		warnings = append(warnings, w)
		out.NextActions = append(out.NextActions, events.DropNextAction)
		degrade()
	}
	// A missing daemon is the cause and quiet sources the symptom, so it
	// is listed first.
	if p := in.DaemonMissing; p != nil {
		warnings = append([]string{p.Warning}, warnings...)
		out.NextActions = append(out.NextActions, p.NextAction)
		degrade()
	}
	if len(warnings) > 0 {
		out.NextActions = append(out.NextActions, config.StaleIngestionNextAction)
		degrade()
	}
	// An outdated binary qualifies every other answer, so it leads.
	if p := in.Drift; p != nil {
		warnings = append([]string{p.Warning}, warnings...)
		out.NextActions = append(out.NextActions, p.NextAction)
		degrade()
	}
	out.Warnings = warnings
	out.Insight = presentation.ForStatus(out.State)
	return out
}

// StaleWarnings words each quiet source.
func StaleWarnings(stale []config.StaleSource) []string {
	if len(stale) == 0 {
		return nil
	}
	out := make([]string, 0, len(stale))
	for _, s := range stale {
		out = append(out, s.Warning())
	}
	return out
}
