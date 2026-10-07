package config

import (
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
)

// CoachingConfig tunes the waste detector behind `tokenops replay
// --workflow`, the tokenops_records (view=workflow) MCP tool, and the dashboard
// workflow view.
type CoachingConfig struct {
	// Delivery selects how far coaching goes. It is a ladder graded by
	// interference — how much of your session the coach is allowed to
	// take — and each rung adds a channel to the one below:
	//
	//   observe    — records everything, answers when asked. `tokenops
	//                coach prompts`, `coach replies`, `dx`, and the MCP
	//                coaching tools. The hooks stay installed but say
	//                nothing: they keep their ledgers, so `coach stats`
	//                still shows what you are missing before you let
	//                them speak.
	//   advise     — observe, plus the coach speaks unprompted but never
	//                blocks: coach-hook nudges as session cost crosses a
	//                budget fraction. Advice you can ignore. The default.
	//   intervene  — advise, plus the coach acts: read-guard refuses a
	//                redundant re-read before it costs a token.
	//
	// The rungs are graded by interference rather than by who initiated,
	// because that is the question an operator actually has. "Does it
	// speak without being asked" puts a non-blocking nudge and a refused
	// tool call on the same rung, and those are not remotely the same
	// imposition.
	//
	// Note this is NOT Config.Mode. Mode decides whether TokenOps
	// intervenes in *traffic* — routing rules on the proxy, the spend
	// watcher. Delivery decides whether it intervenes in your *session*.
	// An operator can reasonably want either without the other.
	//
	// Empty means DeliveryAdvise, which is what the hooks did before this
	// key existed: coach-hook nudged, read-guard observed. Upgrading
	// changes nothing until you say so.
	Delivery string `yaml:"delivery,omitempty"`

	// Quiet rate-limits the coach's *proactive* channel — the nudges it
	// speaks without being asked. It has no effect on anything you ask
	// for: a direct question is never an interruption.
	Quiet QuietConfig `yaml:"quiet,omitempty"`

	// ContextLimits override the waste detector's context thresholds per
	// workflow-ID prefix. A matching entry replaces the built-in
	// profiles ("claude-code:", "codex:"); zero fields inherit the
	// detector defaults.
	ContextLimits []ContextLimitConfig `yaml:"context_limits"`
}

// QuietConfig bounds how often the coach may speak unprompted in one
// session. Only meaningful at delivery advise and intervene, where
// something speaks at all.
//
// Each individual finding already latches — a budget tier fires once per
// session and then stays quiet — so this is not about one finding
// repeating itself. It is about two *different* findings landing back to
// back and reading, to the operator, as nagging.
//
// Both knobs default to off, and off means the per-finding latches are
// the whole policy, which is what the hooks did before this key existed.
type QuietConfig struct {
	// MinInterval is the floor between two proactive nudges in one
	// session. A nudge suppressed by the floor is deferred, not dropped:
	// its finding stays unlatched and speaks at the next opportunity once
	// the floor has passed. Zero means no floor.
	MinInterval time.Duration `yaml:"min_interval,omitempty"`

	// MaxPerSession caps how many proactive nudges one session may carry.
	// Unlike the floor this drops rather than defers — a cap that queues
	// is not a cap. Zero means no cap, deferring entirely to the
	// per-finding latches.
	MaxPerSession int `yaml:"max_per_session,omitempty"`
}

// Validate rejects a quiet policy that cannot mean anything.
func (q QuietConfig) Validate() error {
	if q.MinInterval < 0 {
		return fmt.Errorf("coaching.quiet.min_interval must not be negative, got %s", q.MinInterval)
	}
	if q.MaxPerSession < 0 {
		return fmt.Errorf("coaching.quiet.max_per_session must not be negative, got %d", q.MaxPerSession)
	}
	return nil
}

// Delivery values for CoachingConfig.Delivery, in ascending order of
// interference.
const (
	DeliveryObserve   = "observe"
	DeliveryAdvise    = "advise"
	DeliveryIntervene = "intervene"
)

// ParseDelivery normalises a delivery level, falling back to reactive for
// anything unrecognised. Callers that need to reject a typo rather than
// absorb it use ValidateDelivery.
func ParseDelivery(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case DeliveryObserve:
		return DeliveryObserve
	case DeliveryIntervene:
		return DeliveryIntervene
	default:
		return DeliveryAdvise
	}
}

// ValidateDelivery reports whether s names a delivery level. Empty is
// valid and means the default.
func ValidateDelivery(s string) error {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", DeliveryObserve, DeliveryAdvise, DeliveryIntervene:
		return nil
	default:
		return fmt.Errorf("coaching.delivery %q: want one of %s, %s, %s",
			s, DeliveryObserve, DeliveryAdvise, DeliveryIntervene)
	}
}

// Delivery resolves the configured level, applying the default.
func (c CoachingConfig) DeliveryLevel() string { return ParseDelivery(c.Delivery) }

// AllowsPull reports whether coaching answers a direct question, through
// the CLI verbs or the MCP tools. True at every level: asking is never an
// interruption, and a tool that refuses to answer a direct question is a
// worse experience than one that stays quiet.
func (c CoachingConfig) AllowsPull() bool { return true }

// AllowsAdvice reports whether the coach may speak unprompted without
// blocking anything — the coach-hook nudge.
func (c CoachingConfig) AllowsAdvice() bool {
	switch c.DeliveryLevel() {
	case DeliveryAdvise, DeliveryIntervene:
		return true
	default:
		return false
	}
}

// AllowsIntervention reports whether the coach may interfere with the
// agent's work — read-guard refusing a redundant re-read.
func (c CoachingConfig) AllowsIntervention() bool {
	return c.DeliveryLevel() == DeliveryIntervene
}

// ContextLimitConfig is one per-prefix threshold override.
type ContextLimitConfig struct {
	WorkflowPrefix           string `yaml:"workflow_prefix"`
	MaxContextTokens         int64  `yaml:"max_context_tokens"`
	ContextGrowthLimitTokens int64  `yaml:"context_growth_limit_tokens"`
	// ContextGrowthPerStepTokens judges growth as a mean per step rather
	// than a session total; when set, context_growth_limit_tokens is
	// ignored for the prefix.
	ContextGrowthPerStepTokens int64 `yaml:"context_growth_per_step_tokens,omitempty"`
	MaxConsecutiveAgentLoops   int   `yaml:"max_consecutive_agent_loops"`
	SystemRedundancyMin        int   `yaml:"system_redundancy_min"`
	// CompactAtTokens is the context size past which a long stretch
	// without compacting is reported (compact_earlier). Zero keeps the
	// built-in profile's value for the prefix.
	CompactAtTokens int64 `yaml:"compact_at_tokens,omitempty"`
}

// WasteConfig maps coaching.context_limits into the waste detector's
// domain config. Shared by every adapter (CLI replay, MCP, dashboard)
// so all surfaces apply identical thresholds.
func (c CoachingConfig) WasteConfig() waste.Config {
	if len(c.ContextLimits) == 0 {
		return waste.Config{}
	}
	profiles := make([]waste.Profile, 0, len(c.ContextLimits))
	for _, l := range c.ContextLimits {
		profiles = append(profiles, waste.Profile{
			WorkflowPrefix:             l.WorkflowPrefix,
			MaxContextTokens:           l.MaxContextTokens,
			ContextGrowthLimitTokens:   l.ContextGrowthLimitTokens,
			ContextGrowthPerStepTokens: l.ContextGrowthPerStepTokens,
			CompactAtTokens:            l.CompactAtTokens,
			MaxConsecutiveAgentLoops:   l.MaxConsecutiveAgentLoops,
			SystemRedundancyMin:        l.SystemRedundancyMin,
		})
	}
	return waste.Config{Profiles: profiles}
}
