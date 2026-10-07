package bootstrap

import (
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/infra/cursorstate"
	opencodestore "go.klarlabs.de/tokenops/internal/infra/opencodedb"
)

// The agent-DX readers run from every surface — the CLI's dx, story and
// verify commands, the MCP tools, the daemon's background analysis — and
// all of them reach this package, so the Cursor and opencode store
// readers are wired once, when the binary starts, before anything
// extracts. A process that never imports bootstrap reports a store it
// finds as agentdx.ErrNoCursorStore or ErrNoOpencodeStore rather than
// reading it as idle.
func init() {
	agentdx.UseCursorStore(cursorstate.Store{})
	agentdx.UseOpencodeStore(opencodestore.Store{})
}
