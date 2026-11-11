package mcp

import (
	"context"
	"time"
)

// DaemonJSON reads one of the daemon's API routes into out.
type DaemonJSON func(ctx context.Context, path string, out any) error

// daemonPlanTimeout bounds a plan read from the daemon. The glance is
// answered from the daemon's last result at once; headroom and the advice
// are computed per request, at background priority.
const daemonPlanTimeout = 20 * time.Second

// daemonPlanLimit bounds a plan answer's body.
const daemonPlanLimit = 8 << 20

// DaemonPlanReader reads the plan routes of the daemon at baseURL.
func DaemonPlanReader(baseURL string) DaemonJSON {
	return DaemonReader(baseURL, daemonPlanTimeout)
}

// DaemonReader reads the daemon's routes at baseURL, waiting up to timeout
// for each answer.
func DaemonReader(baseURL string, timeout time.Duration) DaemonJSON {
	return func(ctx context.Context, path string, out any) error {
		return getDaemonJSONWithin(ctx, baseURL, path, timeout, daemonPlanLimit, out)
	}
}

// fromDaemon fills out from the daemon's path and reports whether it
// answered; false leaves the caller to compute the answer itself.
//
// The glance and the plan views are the daemon's to answer (ADR 0010).
// tokenops serve is a child of the MCP client and lives as long as it
// does, through every upgrade: computing them here answered with the code
// of whatever release the client started it with, days old, while the
// daemon and the menu bar ran the current one. It also read the store a
// second time for answers the daemon already holds, and left out the
// findings the operator had dismissed. A daemon that does not answer, or
// none at all, still gets an answer computed here.
func (d PlanDeps) fromDaemon(ctx context.Context, path string, out any) bool {
	if d.Daemon == nil {
		return false
	}
	return d.Daemon(ctx, path, out) == nil
}
