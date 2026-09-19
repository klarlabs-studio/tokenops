package mcp

import (
	"context"
	"encoding/json"

	mcpgo "go.klarlabs.de/mcp"
	"go.klarlabs.de/mcp/protocol"

	"go.klarlabs.de/tokenops/internal/contexts/spend/session"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SessionMiddleware records one session.Tracker ping for every
// `tools/call` request that names a tokenops_* tool, regardless of
// which specific handler runs. This makes the MCP-side activity
// signal uniform across the surface instead of relying on each
// handler to remember to call Tracker.Record.
//
// provider is asked on every call rather than read once: it is inferred
// from the configured plans, and a plan bound after the server started —
// by `tokenops plan set` or tokenops_plan_set — used to leave every ping
// stamped "unknown" until the MCP client restarted the server.
//
// Empty tracker (or a tools/call against an unrelated tool name)
// degrades to a pass-through.
func SessionMiddleware(t *session.Tracker, provider func() eventschema.Provider) mcpgo.Middleware {
	return func(next mcpgo.MiddlewareHandlerFunc) mcpgo.MiddlewareHandlerFunc {
		return func(ctx context.Context, req *protocol.Request) (*protocol.Response, error) {
			if t != nil && req != nil && req.Method == "tools/call" {
				if name := extractToolName(req.Params); name != "" {
					p := eventschema.ProviderUnknown
					if provider != nil {
						p = provider()
					}
					t.Record(ctx, session.Options{
						Provider:    p,
						SourceLabel: "mcp-session",
					}, name)
				}
			}
			return next(ctx, req)
		}
	}
}

// extractToolName pulls the "name" field out of a tools/call params
// blob without unmarshalling into the full mcp-go params type — the
// middleware just needs the identifier, and we don't want to take a
// hard dependency on internal mcp-go shapes.
func extractToolName(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var probe struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	return probe.Name
}
