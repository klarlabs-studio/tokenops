package state

import (
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/authority"
	"go.klarlabs.de/tokenops/internal/config"
)

// Mode is the operating mode and what it lets each subsystem do.
type Mode struct {
	// Mode is the value `tokenops mode` accepts: passive or active. The
	// authority rung is reported beside it rather than in its place.
	Mode         string                `json:"mode"`
	Authority    string                `json:"authority"`
	AnythingActs bool                  `json:"anything_acts"`
	Subsystems   []authority.Subsystem `json:"subsystems"`
	Budgets      int                   `json:"budgets"`
	RoutingRules int                   `json:"routing_rules"`
	// HeldBack names subsystems configured to do more than the mode
	// allows.
	HeldBack     []string `json:"held_back,omitempty"`
	HeldBackNote string   `json:"held_back_note,omitempty"`
}

// ModeOf reports cfg's mode and authority.
func ModeOf(cfg config.Config) Mode {
	a := authority.Report(cfg)
	mode := cfg.Mode
	if mode == "" {
		mode = config.ModePassive
	}
	out := Mode{
		Mode:         strings.ToLower(mode),
		Authority:    a.Daemon.String(),
		AnythingActs: a.AnythingActs(),
		Subsystems:   a.Subsystems,
		Budgets:      len(cfg.Budgets),
		RoutingRules: len(cfg.Optimizer.RoutingRules),
	}
	for _, s := range a.HeldBack() {
		out.HeldBack = append(out.HeldBack, s.Name)
	}
	if len(out.HeldBack) > 0 {
		out.HeldBackNote = "these are configured to do more than the daemon's mode allows; " +
			"raising mode to active lets them act"
	}
	return out
}
