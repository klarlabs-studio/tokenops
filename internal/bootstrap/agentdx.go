package bootstrap

import (
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/infra/cursorstate"
)

// The agent-DX readers run from every surface — the CLI's dx, story and
// verify commands, the MCP tools, the daemon's background analysis — and
// all of them reach this package, so the Cursor store reader is wired
// once, when the binary starts, before anything extracts. A process that
// never imports bootstrap reports a Cursor store it finds as
// agentdx.ErrNoCursorStore rather than reading it as idle.
func init() {
	agentdx.UseCursorStore(cursorstate.Store{})
}
