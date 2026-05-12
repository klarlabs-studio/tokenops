// Package rbac defines the role taxonomy + permission grid TokenOps
// uses to gate API operations. The MVP is observe-only: middleware
// stamps the role on the request context, but enforcement is left to
// individual handlers (a "warn now, enforce next" rollout). The
// Permission predicates are exhaustive enough that handlers can flip
// to enforcement by replacing a log line with a 403 response.
package rbac

import (
	"context"
	"errors"
	"net/http"
)

// Role names. Operators map authenticated subjects to a Role via the
// dashauth.Authenticator (or a future OIDC backend).
type Role string

// Built-in roles. The set is intentionally small: TokenOps is a
// local-first daemon, so additional roles (auditor, billing-admin, …)
// are best modelled as future entries here rather than a bespoke
// per-deployment grid.
const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
	// RoleAnonymous applies when no credential matched. Handlers that
	// should be reachable without auth (login, healthz) accept it
	// explicitly via Permission predicates.
	RoleAnonymous Role = "anonymous"
)

// Permission identifies one auditable operation. Names mirror the API
// surface (verb + resource) so a glance at the permission grid below
// answers "who can call what?".
type Permission string

const (
	PermSpendRead         Permission = "spend.read"
	PermSpendForecastRead Permission = "spend.forecast.read"
	PermWorkflowRead      Permission = "workflow.read"
	PermOptimizationRead  Permission = "optimization.read"
	PermOptimizationApply Permission = "optimization.apply"
	PermReplayRun         Permission = "replay.run"
	PermCacheManage       Permission = "cache.manage"
	PermConfigRead        Permission = "config.read"
	PermConfigWrite       Permission = "config.write"
	PermAuditRead         Permission = "audit.read"
)

// grid is the canonical role → permissions map. The grid is closed:
// any permission not listed here is denied for that role.
var grid = map[Role]map[Permission]bool{
	RoleAdmin: {
		PermSpendRead:         true,
		PermSpendForecastRead: true,
		PermWorkflowRead:      true,
		PermOptimizationRead:  true,
		PermOptimizationApply: true,
		PermReplayRun:         true,
		PermCacheManage:       true,
		PermConfigRead:        true,
		PermConfigWrite:       true,
		PermAuditRead:         true,
	},
	RoleOperator: {
		PermSpendRead:         true,
		PermSpendForecastRead: true,
		PermWorkflowRead:      true,
		PermOptimizationRead:  true,
		PermOptimizationApply: true,
		PermReplayRun:         true,
		PermCacheManage:       true,
		PermConfigRead:        true,
		// No config.write or audit.read for operator.
	},
	RoleViewer: {
		PermSpendRead:         true,
		PermSpendForecastRead: true,
		PermWorkflowRead:      true,
		PermOptimizationRead:  true,
		PermConfigRead:        true,
	},
	RoleAnonymous: {
		// Nothing by default. Handlers must explicitly bypass the
		// middleware (e.g. /healthz) when anonymous access is allowed.
	},
}

// Allows reports whether role can perform perm.
func Allows(role Role, perm Permission) bool {
	perms, ok := grid[role]
	if !ok {
		return false
	}
	return perms[perm]
}

// Permissions returns the set of permissions granted to role. The
// returned slice is owned by the caller and safe to mutate.
func Permissions(role Role) []Permission {
	perms := grid[role]
	out := make([]Permission, 0, len(perms))
	for p := range perms {
		out = append(out, p)
	}
	return out
}

// AllRoles returns every defined role.
func AllRoles() []Role {
	return []Role{RoleAdmin, RoleOperator, RoleViewer, RoleAnonymous}
}

// --- request-context plumbing ----------------------------------------

type roleKey struct{}

// WithRole stamps role on ctx. Middleware uses this to propagate the
// authenticated role from the auth layer to handlers.
func WithRole(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, roleKey{}, role)
}

// FromContext returns the role stamped on ctx, or RoleAnonymous when
// none is present.
func FromContext(ctx context.Context) Role {
	if v, ok := ctx.Value(roleKey{}).(Role); ok {
		return v
	}
	return RoleAnonymous
}

// ErrForbidden is returned by Require when the request lacks the
// required permission. Handlers translate this to HTTP 403.
var ErrForbidden = errors.New("rbac: forbidden")

// Require returns nil when the role on ctx is allowed perm; otherwise
// ErrForbidden. Useful for non-HTTP entry points (background jobs, MCP
// tools) that want the same predicate without an http.ResponseWriter.
func Require(ctx context.Context, perm Permission) error {
	if Allows(FromContext(ctx), perm) {
		return nil
	}
	return ErrForbidden
}

// HTTPMiddleware returns an http.Handler middleware that gates next on
// perm. A failed check writes 403 with a short JSON body. Pair with
// dashauth.Authenticator.Middleware (which provides the role) for end
// to end protection.
func HTTPMiddleware(perm Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Allows(FromContext(r.Context()), perm) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	})
}
