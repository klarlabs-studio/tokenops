// Package mcp hosts the TokenOps Model Context Protocol surface. It
// registers the TokenOps tool set (spend, forecast, rules, parity,
// control) on top of go.klarlabs.de/mcp, which owns the
// JSON-RPC framing, schema generation, stdio/HTTP transports and
// middleware. This package is a thin adapter — tool input structs and
// handler bodies are defined here, the protocol layer is upstream.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	mcpgo "go.klarlabs.de/mcp"

	"go.klarlabs.de/tokenops/internal/version"
)

// Server is an alias for the upstream mcp-go server type so callers can
// pass either Build- or Tool-style handles around without knowing the
// upstream import path.
type Server = mcpgo.Server

// NewServer constructs a Server announcing the TokenOps name/version
// pair and the tools capability. The logger is accepted for API
// parity with earlier revisions; mcp-go takes its own logger via
// middleware when needed.
func NewServer(name, ver string, _ *slog.Logger) *Server {
	if ver == "" {
		ver = version.Version
	}
	return mcpgo.NewServer(mcpgo.ServerInfo{
		Name:    name,
		Version: ver,
		Capabilities: mcpgo.Capabilities{
			Tools: true,
		},
	}, mcpgo.WithTitle("TokenOps"), mcpgo.WithInstructions(instructions))
}

// instructions orient an agent before its first call: which tool answers
// what, and what TokenOps will and will not do on its own.
const instructions = `TokenOps watches the operator's AI subscription plans, sessions and spend, and coaches how they are used. It reads local data; it never sees prompts or file contents beyond what the operator's own clients write locally, and it never changes the caller's model.

Before a long task, call tokenops_glance: each plan's windows with their pace, and the coach's findings. To bracket a task, call tokenops_prepare_work before and tokenops_review_work after with the workflow_id it returns, and record how it went with tokenops_outcome.

For questions: tokenops_spend (what was spent), tokenops_sessions (how sessions go), tokenops_records (what TokenOps recorded), tokenops_status (whether TokenOps is working), tokenops_explain (what a figure means, or why a decision was made). Most tools pick their slice with a view.

tokenops_routing, tokenops_configure, tokenops_coach and tokenops_experiment change settings: confirm with the operator first.`

// ServeStdio runs srv over the JSON-RPC/stdio transport from mcp-go.
// It blocks until ctx is cancelled or the input stream closes.
// Optional middleware (e.g. SessionMiddleware) chains in front of the
// per-tool handlers.
func ServeStdio(ctx context.Context, srv *Server, mws ...mcpgo.Middleware) error {
	if len(mws) == 0 {
		return mcpgo.ServeStdio(ctx, srv)
	}
	return mcpgo.ServeStdio(ctx, srv, mcpgo.WithMiddleware(mws...))
}

// jsonString marshals v indented, returning a string suitable for the
// MCP "text" content block.
func jsonString(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return string(b)
}
