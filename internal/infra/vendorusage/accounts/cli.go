package accounts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// A vendor CLI source runs the operator's own CLI, which signs its own
// request with the sign-in it keeps (ADR 0011 §1): TokenOps never sees the
// credential. The CLI is run with fixed arguments, no shell, an empty
// stdin (so it cannot wait on a prompt) and a deadline, and its output is
// bounded. Its output is parsed, never logged.

// cliTimeout bounds one run of a vendor CLI.
const cliTimeout = 20 * time.Second

// cliMaxOutput bounds what is kept of a CLI's output.
const cliMaxOutput = 1 << 20

// cliInstallDirs are where installers put CLIs that launchd's PATH hides.
func cliInstallDirs(home string) []string {
	return []string{
		filepath.Join(home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin",
		filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".bun", "bin"),
		filepath.Join(home, ".amp", "bin"), filepath.Join(home, ".cargo", "bin"),
	}
}

// locateCLI finds a vendor CLI: the explicit override when set (an
// override that is not an executable finds nothing, rather than another
// installation), else on PATH, else where installers put it.
func locateCLI(name, override string) (string, bool) {
	if override != "" {
		return override, isExecutable(override)
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	for _, dir := range cliInstallDirs(home) {
		if p := filepath.Join(dir, name); isExecutable(p) {
			return p, true
		}
	}
	return "", false
}

func isExecutable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// errCLIFailed is a CLI that ran and exited with an error; the output is
// in the result for the caller to classify, never in the error.
var errCLIFailed = errors.New("the CLI exited with an error")

// runCLI runs bin with args and returns stdout and stderr together, ANSI
// escapes removed. env is added to the inherited environment. A run that
// exits non-zero returns its output and errCLIFailed, so the caller can
// tell a sign-in refusal from a failure.
func runCLI(ctx context.Context, timeout time.Duration, bin string, env []string, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = cliTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // the operator's own vendor CLI, fixed arguments, no shell
	cmd.Stdin = nil
	cmd.Env = append(append(os.Environ(), "NO_COLOR=1"), env...)
	ownProcessGroup(cmd)
	cmd.WaitDelay = time.Second
	if dir, err := os.MkdirTemp("", "tokenops-cli-"); err == nil {
		// An empty working directory: the CLI reads no project of ours.
		defer func() { _ = os.RemoveAll(dir) }()
		cmd.Dir = dir
	}
	var out limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := stripANSI(out.String())
	switch {
	case ctx.Err() != nil:
		return "", fmt.Errorf("accounts: %s did not answer within %s", filepath.Base(bin), timeout)
	case err != nil:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return text, fmt.Errorf("accounts: %s: %w (exit %d)", filepath.Base(bin), errCLIFailed, ee.ExitCode())
		}
		return "", fmt.Errorf("accounts: run %s: %w", filepath.Base(bin), err)
	}
	return text, nil
}

// limitedBuffer keeps the first cliMaxOutput bytes written to it.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := cliMaxOutput - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// stripANSI removes terminal escape sequences and carriage returns.
func stripANSI(s string) string {
	s = ansiEscape.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, "\r", "")
}

// mentions reports whether text contains any of phrases, ignoring case:
// how a CLI says it is not signed in.
func mentions(text string, phrases ...string) bool {
	lower := strings.ToLower(text)
	for _, p := range phrases {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// cliAuthError wraps a sign-in refusal as usage.ErrAuth.
func cliAuthError(name string) error {
	return fmt.Errorf("%w (%s is not signed in)", usage.ErrAuth, name)
}
