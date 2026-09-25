// Package presentation defines compact, surface-neutral insight models.
package presentation

// StatusInsight is the concise interpretation shared by TokenOps surfaces.
// Detailed status fields remain alongside it for explanation and diagnosis.
type StatusInsight struct {
	Level   string `json:"level" jsonschema:"description=clear when service checks pass, attention when running with reduced coverage, action_required when configuration blockers remain, unavailable when readiness has not been established"`
	Summary string `json:"summary" jsonschema:"description=concise interpretation of readiness, blockers, and warnings; detailed evidence remains in sibling fields"`
}

// ForStatus summarizes the service state reported by readiness checks only.
// In particular, a clear state says nothing about current work quality or
// AI-resource pressure.
func ForStatus(state string) StatusInsight {
	switch state {
	case "ready":
		return StatusInsight{Level: "clear", Summary: "Service checks pass; this status does not assess current work or resource pressure."}
	case "degraded":
		return StatusInsight{Level: "attention", Summary: "TokenOps is running with reduced coverage; inspect blockers, warnings, and next_actions."}
	case "not_configured":
		return StatusInsight{Level: "action_required", Summary: "Setup is incomplete; resolve the listed blockers using next_actions."}
	default:
		return StatusInsight{Level: "unavailable", Summary: "Readiness is not established; inspect blockers and next_actions."}
	}
}
