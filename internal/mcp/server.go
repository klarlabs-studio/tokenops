// Package mcp hosts the TokenOps Model Context Protocol server. It
// exposes the same data the CLI prints (spend, forecast, replay, waste
// analysis) as MCP tools so an LLM client can query the local event
// store directly.
//
// The server speaks JSON-RPC 2.0 over stdio and implements only the
// subset of MCP needed for tool use: initialize, tools/list, tools/call,
// and ping. Full protocol coverage (resources, prompts, sampling) is
// intentionally out of scope for this MVP — the rest of the protocol can
// be layered on without changing the tool surface.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// ProtocolVersion is the MCP protocol revision the server claims to
// implement. Clients negotiate compatibility on initialize.
const ProtocolVersion = "2024-11-05"

// Server is an MCP server. Construct via NewServer; register tools with
// AddTool before calling Serve.
type Server struct {
	name    string
	version string
	logger  *slog.Logger

	mu    sync.RWMutex
	tools map[string]*Tool
}

// Tool is one tool advertised by the server. Handler is invoked on a
// tools/call with the parsed arguments JSON.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Handler     ToolHandler     `json:"-"`
}

// ToolHandler runs a tool. The returned string is wrapped in MCP's
// {content:[{type:"text",text:"..."}]} response shape.
type ToolHandler func(ctx context.Context, args json.RawMessage) (string, error)

// NewServer constructs a Server. Pass nil logger to silence diagnostics.
func NewServer(name, version string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{
		name:    name,
		version: version,
		logger:  logger,
		tools:   make(map[string]*Tool),
	}
}

// AddTool registers t. Replacing an existing tool by name is allowed.
func (s *Server) AddTool(t *Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools[t.Name] = t
}

// Serve reads JSON-RPC requests from in, writes responses to out, and
// returns when in returns EOF or ctx is cancelled.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	enc := json.NewEncoder(out)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			s.logger.Warn("mcp: parse error", "err", err)
			continue
		}
		resp := s.dispatch(ctx, &req)
		if resp == nil {
			// Notifications expect no response.
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// --- JSON-RPC plumbing -------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (s *Server) dispatch(ctx context.Context, req *rpcRequest) *rpcResponse {
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		return errorResponse(req.ID, -32600, "invalid jsonrpc version")
	}
	// Notifications carry no ID and expect no response.
	if len(req.ID) == 0 {
		return nil
	}
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "ping":
		return &rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		return errorResponse(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *Server) handleInitialize(req *rpcRequest) *rpcResponse {
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": ProtocolVersion,
			"serverInfo": map[string]string{
				"name":    s.name,
				"version": s.version,
			},
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
		},
	}
}

func (s *Server) handleToolsList(req *rpcRequest) *rpcResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tools := make([]*Tool, 0, len(s.tools))
	for _, t := range s.tools {
		tools = append(tools, t)
	}
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"tools": tools,
		},
	}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (s *Server) handleToolsCall(ctx context.Context, req *rpcRequest) *rpcResponse {
	var p callParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorResponse(req.ID, -32602, "invalid params: "+err.Error())
	}
	s.mu.RLock()
	tool, ok := s.tools[p.Name]
	s.mu.RUnlock()
	if !ok {
		return errorResponse(req.ID, -32602, "tool not found: "+p.Name)
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := tool.Handler(callCtx, p.Arguments)
	if err != nil {
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"isError": true,
				"content": []map[string]any{
					{"type": "text", "text": err.Error()},
				},
			},
		}
	}
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": out},
			},
		},
	}
}

func errorResponse(id json.RawMessage, code int, message string) *rpcResponse {
	return &rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
}

// ErrToolFailed wraps a handler error so callers (mainly tests) can
// distinguish an authentic tool failure from an unrelated internal one.
var ErrToolFailed = errors.New("mcp: tool failed")
