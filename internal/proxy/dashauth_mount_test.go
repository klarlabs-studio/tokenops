package proxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

// denyAll stands in for the dashboard authenticator. The real one accepts a
// bearer token, a query token or a session cookie; what this test asserts is
// narrower and does not depend on which: every protected route must reach the
// middleware at all. A middleware that refuses everything makes "reached" and
// "did not reach" visible as 401 vs 200.
type denyAll struct{}

func (denyAll) Middleware(http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
}

// TestEveryAPIRouteIsGatedByDashAuth pins the invariant that /api/* is
// credentialed.
//
// The routes below were registered on the outer mux while /api/ was handled by
// the authenticated sub-mux. ServeMux gives an exact pattern precedence over a
// wildcard, so the wildcard never saw them and they served unauthenticated with
// auth correctly configured — /api/audit, the security audit log, among them.
// Loopback binding was the only thing limiting the exposure, and the daemon
// supports binding beyond it.
func TestEveryAPIRouteIsGatedByDashAuth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithDashAuth(denyAll{}),
		// Wired the way the daemon wires them: analytics present, so the
		// storage-disabled stubs stay off and every handler registers for
		// real. The handlers' own dependencies stay nil because denyAll
		// answers before any of them is reached.
		WithAnalytics(&AnalyticsHandlers{}),
		WithRules(&RulesHandlers{}),
		WithAudit(&AuditHandlers{}),
		WithEventCounts(func() map[string]int64 { return map[string]int64{"k": 1} }),
	)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})
	waitListening(t, srv.Addr())

	for _, path := range []string{
		"/api/audit",
		"/api/rules/analyze",
		"/api/rules/conflicts",
		"/api/rules/compress",
		"/api/rules/inject",
		"/api/domain-events",
	} {
		resp, err := http.Get("http://" + srv.Addr() + path)
		if err != nil {
			t.Errorf("GET %s: %v", path, err)
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without a credential = %d, want 401: route bypasses dashboard auth",
				path, resp.StatusCode)
		}
	}
}

// TestHealthRoutesStayOpenWithDashAuth is the other half of the contract: a
// probe must never need a credential, so gating /api/* must not sweep these up.
func TestHealthRoutesStayOpenWithDashAuth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	srv := New("127.0.0.1:0",
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithShutdownTimeout(time.Second),
		WithDashAuth(denyAll{}),
	)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	})
	waitListening(t, srv.Addr())

	for _, path := range []string{"/healthz", "/readyz", "/version"} {
		resp, err := http.Get("http://" + srv.Addr() + path)
		if err != nil {
			t.Errorf("GET %s: %v", path, err)
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		// Not 200: /readyz answers 503 on a bare config, which is the
		// endpoint doing its job. What matters is that denyAll never saw
		// the request.
		if resp.StatusCode == http.StatusUnauthorized {
			t.Errorf("GET %s = 401: health probes must not require a credential", path)
		}
	}
}
