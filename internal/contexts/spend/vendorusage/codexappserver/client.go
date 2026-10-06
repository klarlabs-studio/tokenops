// Package codexappserver reads Codex's plan windows by asking Codex itself:
// `codex app-server` speaks JSON-RPC over stdio, and its
// account/rateLimits/read answers with the windows Codex signs in for
// (ADR 0011, a harness feed). TokenOps never sees a credential; Codex
// authenticates its own request, as CodexBar's CLI source does.
package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Window is one rate-limit window as Codex reports it.
type Window struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins int64   `json:"windowDurationMins"`
	ResetsAt           int64   `json:"resetsAt"`
}

// Snapshot is the part of account/rateLimits/read this source keeps.
type Snapshot struct {
	PlanType  string  `json:"planType"`
	Primary   *Window `json:"primary"`
	Secondary *Window `json:"secondary"`
	// RateLimitReachedType is set when a limit has been hit.
	RateLimitReachedType string `json:"rateLimitReachedType"`
}

// ErrNotSignedIn means Codex answered without a plan: an API-key login or
// none at all.
var ErrNotSignedIn = errors.New("codex-app-server: Codex reports no plan windows; it is not signed in with ChatGPT")

// rpcTimeout bounds one conversation with the app server.
const rpcTimeout = 20 * time.Second

// Conn is a running app server's stdio.
type Conn interface {
	io.Writer
	io.Reader
	Close() error
}

// Dial starts the app server; tests replace it.
type Dial func(ctx context.Context) (Conn, error)

// Command dials `codex -s read-only -a never app-server`: read-only and
// never approving anything, since it only answers questions here.
func Command(bin string) Dial {
	return func(ctx context.Context) (Conn, error) {
		cmd := exec.CommandContext(ctx, bin, "-s", "read-only", "-a", "never", "app-server") //nolint:gosec // the operator's own codex
		in, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("codex-app-server: start %s: %w", bin, err)
		}
		return &procConn{Writer: in, Reader: out, stdin: in, cmd: cmd}, nil
	}
}

type procConn struct {
	io.Writer
	io.Reader
	stdin io.Closer
	cmd   *exec.Cmd
}

func (p *procConn) Close() error {
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
	return nil
}

// Read asks the app server for the account's rate limits.
func Read(ctx context.Context, dial Dial) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	conn, err := dial(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = conn.Close() }()
	enc, lines := json.NewEncoder(conn), bufio.NewScanner(conn)
	lines.Buffer(make([]byte, 64*1024), 4<<20)
	if err := enc.Encode(rpcRequest{ID: 1, Method: "initialize", Params: map[string]any{
		"clientInfo": map[string]string{"name": "tokenops", "version": "1"},
	}}); err != nil {
		return Snapshot{}, err
	}
	if _, err := await(ctx, lines, 1); err != nil {
		return Snapshot{}, err
	}
	if err := enc.Encode(rpcRequest{Method: "initialized"}); err != nil {
		return Snapshot{}, err
	}
	if err := enc.Encode(rpcRequest{ID: 2, Method: "account/rateLimits/read", Params: map[string]any{
		"excludeResetCreditDetails": true,
	}}); err != nil {
		return Snapshot{}, err
	}
	raw, err := await(ctx, lines, 2)
	if err != nil {
		return Snapshot{}, err
	}
	var res struct {
		RateLimits Snapshot `json:"rateLimits"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return Snapshot{}, fmt.Errorf("codex-app-server: unreadable rate limits: %w", err)
	}
	if res.RateLimits.Primary == nil && res.RateLimits.Secondary == nil {
		return Snapshot{}, ErrNotSignedIn
	}
	return res.RateLimits, nil
}

type rpcRequest struct {
	ID     int    `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// await reads lines until the response to id, skipping notifications.
func await(ctx context.Context, lines *bufio.Scanner, id int) (json.RawMessage, error) {
	for lines.Scan() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var m struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(lines.Bytes(), &m) != nil || m.ID == nil || *m.ID != id {
			continue
		}
		if m.Error != nil {
			return nil, fmt.Errorf("codex-app-server: %s", m.Error.Message)
		}
		return m.Result, nil
	}
	if err := lines.Err(); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("codex-app-server: no answer in %s", rpcTimeout)
	}
	return nil, errors.New("codex-app-server: the app server closed without answering")
}

// Locate finds the codex binary: on PATH, else where installers put it.
// The daemon runs under launchd with a minimal PATH, which would hide a
// codex the operator's shell finds.
func Locate(home string) (string, bool) {
	if p, err := exec.LookPath("codex"); err == nil {
		return p, true
	}
	for _, dir := range []string{
		filepath.Join(home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin",
		filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".bun", "bin"),
	} {
		p := filepath.Join(dir, "codex")
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}
