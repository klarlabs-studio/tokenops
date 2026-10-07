package fireworks

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/fireworks"
)

// fireconnectHelper matches FireConnect's apiKeyHelper, `fireconnect key
// export`, with the pattern FireConnect itself uses to recognise it.
var (
	fireconnectBin    = regexp.MustCompile(`(?:^|[/\\])fireconnect(?:\.mjs)?['"]?(?:\s|$)`)
	fireconnectExport = regexp.MustCompile(`(?:^|\s)key\s+export(?:\s|$)`)
)

// IsFireConnectHelper reports whether an apiKeyHelper is FireConnect's.
func IsFireConnectHelper(cmd string) bool {
	return fireconnectBin.MatchString(cmd) && fireconnectExport.MatchString(cmd)
}

// KeySource finds the Fireworks key the operator already uses, in
// FireConnect's own order: FIREWORKS_API_KEY, then FireConnect's key
// export (its keychain entry). Only FireConnect's helper is ever run; any
// other apiKeyHelper is left alone.
type KeySource struct {
	// Getenv reads the environment; nil uses os.Getenv.
	Getenv func(string) string
	// Helper returns Claude Code's apiKeyHelper command, "" for none.
	Helper func() string
	// Run executes the helper and returns its output; nil runs it with
	// sh -c.
	Run func(ctx context.Context, cmd string) (string, error)
	// Fallback returns a Fireworks key another harness uses (opencode,
	// Codex), "" for none.
	Fallback func() string
}

// Key returns the key, or usage.ErrNoKey.
func (k KeySource) Key(ctx context.Context) (string, error) {
	getenv := k.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if v := strings.TrimSpace(getenv("FIREWORKS_API_KEY")); v != "" {
		return v, nil
	}
	if k.Helper == nil {
		return "", usage.ErrNoKey
	}
	cmd := k.Helper()
	if !IsFireConnectHelper(cmd) {
		if k.Fallback != nil {
			if v := strings.TrimSpace(k.Fallback()); v != "" {
				return v, nil
			}
		}
		return "", usage.ErrNoKey
	}
	run := k.Run
	if run == nil {
		run = runHelper
	}
	out, err := run(ctx, cmd)
	if err != nil {
		// The helper's own message can echo configuration; say only that
		// it failed.
		return "", errors.New("fireworks: FireConnect's key export failed")
	}
	key := strings.TrimSpace(out)
	if key == "" || strings.ContainsAny(key, " \n") {
		return "", errors.New("fireworks: FireConnect's key export returned no key")
	}
	return key, nil
}

func runHelper(ctx context.Context, cmd string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/sh", "-c", cmd).Output() //nolint:gosec // only FireConnect's own helper, matched above
	return string(out), err
}
