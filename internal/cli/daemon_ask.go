package cli

import (
	"context"
	"net/url"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/mcp"
)

// reportDaemonWait bounds asking the daemon for a report that reads weeks
// of history. The daemon keeps what such a report reads between requests;
// here it would be read again, from nothing, so a slow daemon is still the
// faster answer.
const reportDaemonWait = 3 * time.Minute

// askDaemon reads path from the daemon at cfg's listen address into out,
// waiting up to wait, and reports whether it answered. Not answered (no
// daemon, another one, an error), the caller computes the answer itself:
// the daemon serves every surface (ADR 0010), and a command still works
// without one.
func askDaemon(ctx context.Context, cfg config.Config, path string, wait time.Duration, out any) bool {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	// No listen address configured is an empty URL, which the reader
	// refuses: computed here.
	return mcp.DaemonReader(mcp.ConfiguredDaemonURL(cfg), wait)(ctx, path, out) == nil
}

// sinceQuery is since as the daemon's ?since= reads it.
func sinceQuery(since time.Time) string {
	return "since=" + url.QueryEscape(since.UTC().Format(time.RFC3339))
}
