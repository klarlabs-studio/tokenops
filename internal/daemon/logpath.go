package daemon

import "path/filepath"

// LogPath returns where the supervised daemon actually writes its output.
//
// This is not ~/.tokenops/daemon.log. That file is written only when the
// MCP tool spawns a daemon itself; a launchd- or systemd-supervised daemon
// logs wherever its unit redirects stdout. On a machine that used both
// paths over time, the stale one sits next to the event store looking
// authoritative and answers questions about a daemon that stopped running
// months ago.
func LogPath(kind UnitKind, home string) string {
	switch kind {
	case UnitLaunchd:
		return filepath.Join(home, "Library", "Logs", "tokenops.log")
	case UnitSystemd:
		// systemd captures stdout into the journal rather than a file.
		return "journalctl --user -u tokenops"
	default:
		return ""
	}
}
