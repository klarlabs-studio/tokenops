package findings

import (
	"fmt"

	"go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
)

// KindBoundary is a finding about the operator's boundaries.
const KindBoundary = "boundary"

// headroomPause says what the headroom pause did this week, what it would
// have done while in shadow, or, when there is none and a plan to watch,
// how to add one. A pause that cannot see a tool call says so rather than
// reporting nothing it would have done.
func headroomPause(p *boundaries.PauseSummary, g *headroom.Glance) []Finding {
	if p == nil {
		return nil
	}
	if !p.Configured {
		if !watchesAPlan(g) {
			return nil
		}
		return []Finding{{Kind: KindBoundary, Level: LevelInfo,
			Title:    "No headroom pause is set",
			Evidence: "nothing stops an agent as a plan window runs out",
			Action: "`tokenops boundaries headroom-pause` adds one at 90% of the tightest window, in shadow: " +
				"it records what it would have paused and binds nothing."}}
	}
	if !p.PausePoint {
		return []Finding{{Kind: KindBoundary, Level: LevelNotice,
			Title:    "The headroom pause sees no tool calls",
			Evidence: fmt.Sprintf("`%s` is set, but no agent has the boundary hook, so it records nothing", p.Rule),
			Action:   "`tokenops hooks install --boundary` adds it to Claude Code (`--client opencode` for opencode)."}}
	}
	if p.Sessions == 0 {
		return nil
	}
	if p.Stage == "enforce" {
		return []Finding{{Kind: KindBoundary, Level: LevelInfo,
			Title:    fmt.Sprintf("The headroom pause paused %s this week", sessionsWord(p.Sessions)),
			Evidence: fmt.Sprintf("`%s` asks before the next tool call once a plan window is nearly used", p.Rule)}}
	}
	return []Finding{{Kind: KindBoundary, Level: LevelInfo,
		Title:    fmt.Sprintf("The headroom pause would have paused %s this week", sessionsWord(p.Sessions)),
		Evidence: fmt.Sprintf("`%s` is in shadow: it records and binds nothing", p.Rule),
		Action:   fmt.Sprintf("`tokenops boundaries promote %s` makes it pause; nothing changes until you do.", p.Rule)}}
}

func sessionsWord(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

// watchesAPlan reports whether the glance has a plan window to pause on.
func watchesAPlan(g *headroom.Glance) bool {
	if g == nil {
		return false
	}
	for _, r := range g.Headroom.Reports {
		if len(r.Windows) > 0 {
			return true
		}
	}
	return false
}

// pauseSummary reads the headroom pause for cfg; nil when there is no
// config or the boundary ledger cannot be read.
func pauseSummary(cfg *config.Config) *boundaries.PauseSummary {
	if cfg == nil {
		return nil
	}
	p, err := boundaries.HeadroomPause(*cfg, boundaries.Env{}, boundaries.Installed("", ""))
	if err != nil {
		return nil
	}
	return &p
}
