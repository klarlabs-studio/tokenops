package attribution

import (
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/spend/biller"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/infra/claudesettings"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// sessionTailBytes is how much of a transcript's end ClaudeCodeSessionBiller
// reads for the session's latest model: one hook call's worth.
const sessionTailBytes = 256 << 10

// cloudSwitches put Claude Code on a cloud vendor's Claude, billed by that
// cloud rather than Anthropic.
var cloudSwitches = []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"}

// ClaudeCodeSessionBiller is the provider that bills a Claude Code
// session's turns now, for a hook that runs inside it: the endpoint
// Claude Code is pointed at (ANTHROPIC_BASE_URL in getenv, which a hook
// inherits from Claude Code, else Claude Code's settings) and, where that
// endpoint bills by model, the session's latest model (model, or the
// transcript's last turn). With no base URL it is Anthropic. It is "" when
// it cannot tell: a cloud vendor's Claude, a local relay or an unknown
// host.
func ClaudeCodeSessionBiller(getenv func(string) string, transcriptPath, model string) string {
	for _, k := range cloudSwitches {
		if v := strings.TrimSpace(getenv(k)); v != "" && v != "0" && !strings.EqualFold(v, "false") {
			return ""
		}
	}
	base := strings.TrimSpace(getenv("ANTHROPIC_BASE_URL"))
	if base == "" {
		base = strings.TrimSpace(claudesettings.BaseURL())
	}
	if base == "" {
		return string(eventschema.ProviderAnthropic)
	}
	if e, ok := biller.EndpointFor(base); ok && e.Provider == eventschema.ProviderAnthropic {
		return string(eventschema.ProviderAnthropic)
	}
	if model == "" && transcriptPath != "" {
		if turns, _, err := claudecodejsonl.ReadTail(transcriptPath, sessionTailBytes); err == nil && len(turns) > 0 {
			model = turns[len(turns)-1].Model
		}
	}
	if p := biller.ForClaudeCodeTurn(model, base); p != eventschema.ProviderUnknown {
		return string(p)
	}
	return ""
}
