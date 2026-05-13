package mcp

import (
	"context"
	"encoding/json"

	mcpgo "github.com/felixgeelhaar/mcp-go"
	"github.com/felixgeelhaar/mcp-go/protocol"

	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/session"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// SessionMiddleware records one session.Tracker ping for every
// `tools/call` request that names a tokenops_* tool, regardless of
// which specific handler runs. This makes the MCP-side activity
// signal uniform across the surface instead of relying on each
// handler to remember to call Tracker.Record.
//
// Empty tracker (or a tools/call against an unrelated tool name)
// degrades to a pass-through.
func SessionMiddleware(t *session.Tracker, provider eventschema.Provider) mcpgo.Middleware {
	return func(next mcpgo.MiddlewareHandlerFunc) mcpgo.MiddlewareHandlerFunc {
		return func(ctx context.Context, req *protocol.Request) (*protocol.Response, error) {
			if t != nil && req != nil && req.Method == "tools/call" {
				if name := extractToolName(req.Params); name != "" {
					t.Record(ctx, session.Options{
						Provider:    provider,
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
