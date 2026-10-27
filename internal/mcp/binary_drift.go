package mcp

import (
	"fmt"

	"go.klarlabs.de/tokenops/internal/infra/selfexe"
	"go.klarlabs.de/tokenops/internal/version"
)

// StaleServerNextAction is the remediation appended when this MCP server is
// running a binary that has since been replaced.
//
// It names the client, not tokenops: `tokenops serve` is a child of the MCP
// client and lives exactly as long as it does. Neither `tokenops start` nor a
// daemon restart touches it, which is how one agent kept talking to 0.54.3
// for weeks after 0.66.0 was installed.
const StaleServerNextAction = "restart your MCP client (or reconnect its tokenops server, e.g. /mcp in Claude Code) so it spawns the installed 'tokenops serve' — the MCP server is a child of the client, and tokenops cannot restart it"

// BinaryDrift is what a check of the running MCP server's binary found. The
// detection lives in internal/infra/selfexe, shared with the daemon.
type BinaryDrift = selfexe.Drift

// driftWarning renders the operator-facing line, or "" when the binary is current.
func driftWarning(d BinaryDrift) string {
	if !d.OutOfDate {
		return ""
	}
	installed := "has since been removed or replaced"
	if d.Installed != "" {
		installed = "has since been replaced by " + d.Installed
	}
	return fmt.Sprintf(
		"this MCP server is out of date: it is still running tokenops %s from %s, which %s — "+
			"everything installed since is invisible to this agent until the MCP client restarts the server",
		version.String(), d.Running, installed)
}

// binaryDrift runs the injected check, or reports nothing when none is wired.
func binaryDrift(d ControlDeps) BinaryDrift {
	if d.BinaryDrift == nil {
		return BinaryDrift{}
	}
	return d.BinaryDrift()
}
