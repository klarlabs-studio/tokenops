// Package codexappserver starts `codex app-server` for the JSON-RPC
// conversation in internal/contexts/spend/vendorusage/codexappserver, and
// finds the codex binary the operator installed.
package codexappserver

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	rpc "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexappserver"
)

// Command dials `codex -s read-only -a never app-server`: read-only and
// never approving anything, since it only answers questions here.
func Command(bin string) rpc.Dial {
	return func(ctx context.Context) (rpc.Conn, error) {
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
