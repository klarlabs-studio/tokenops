package coach

import (
	"strings"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
)

// CompactAt is the context size past which a stretch without compacting
// counts, for workflows starting with prefix ("claude-code:", "codex:").
// The live compact tip and the compact_earlier finding read it from the
// same place, so they never disagree about where the line is: an
// operator's coaching.context_limits entry when it sets one, otherwise
// the built-in profile.
func CompactAt(cfg config.Config, prefix string) int64 {
	for _, l := range cfg.Coaching.ContextLimits {
		if l.CompactAtTokens > 0 && strings.HasPrefix(prefix, l.WorkflowPrefix) {
			return l.CompactAtTokens
		}
	}
	if p := waste.ProfileFor(prefix); p != nil {
		return p.CompactAtTokens
	}
	return 0
}
