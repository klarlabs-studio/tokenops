// Package keychain reads generic passwords from the macOS Keychain: a
// browser's "Safe Storage" key, Claude Code's "Claude Code-credentials".
//
// There are two reads. Quiet never shows anything: when macOS would ask the
// operator to allow it, it fails with ErrInteractionRequired. Everything
// the daemon does in the background reads quietly, so a prompt only ever
// follows a command the operator started. Prompting asks, through
// /usr/bin/security, and waits for the operator; only setup commands use
// it, after saying which item they read and why.
package keychain

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var (
	// ErrNotFound is an item that does not exist.
	ErrNotFound = errors.New("keychain: item not found")
	// ErrInteractionRequired is a quiet read macOS would have asked the
	// operator to allow. Nothing was shown.
	ErrInteractionRequired = errors.New("keychain: macOS would ask to allow this read")
	// ErrDenied is a prompting read the operator refused or left
	// unanswered.
	ErrDenied = errors.New("keychain: access was not allowed")
	// ErrDisabled is a read the operator turned off (keychain.disabled).
	ErrDisabled = errors.New("keychain: access is disabled (keychain.disabled)")
	// ErrUnsupported is a read on a system without a Keychain.
	ErrUnsupported = errors.New("keychain: not available on this system")
)

// Item names one generic password.
type Item struct {
	Service string
	// Account narrows the match; empty matches any account.
	Account string
}

func (i Item) String() string { return fmt.Sprintf("%q", i.Service) }

// Quiet reads item without any UI.
func Quiet(item Item) (string, error) { return quiet(item) }

// securityNotFound is /usr/bin/security's exit status for a missing item.
const securityNotFound = 44

// Prompting reads item through /usr/bin/security, which asks the operator
// to allow it when the item's access list does not already include
// TokenOps, waiting at most wait for the answer.
func Prompting(ctx context.Context, item Item, wait time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	args := []string{"find-generic-password", "-w", "-s", item.Service}
	if item.Account != "" {
		args = append(args, "-a", item.Account)
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/security", args...).Output() //nolint:gosec // fixed binary; service and account come from TokenOps' own tables
	if err != nil {
		var ee *exec.ExitError
		switch {
		case ctx.Err() != nil:
			return "", fmt.Errorf("%w: the prompt for %s went unanswered after %s", ErrDenied, item, wait)
		case errors.As(err, &ee) && ee.ExitCode() == securityNotFound:
			return "", ErrNotFound
		case errors.As(err, &ee):
			return "", fmt.Errorf("%w: %s (security exit %d)", ErrDenied, item, ee.ExitCode())
		}
		return "", fmt.Errorf("keychain: read %s: %w", item, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Login is an application's sign-in kept as an internet password: the
// account it is for and its secret.
type Login struct {
	Account string
	Secret  string
}

// PromptingInternet reads the internet password for server (Zed keeps its
// sign-in under "https://zed.dev") through /usr/bin/security, which asks
// the operator to allow it, waiting at most wait. Only setup commands use
// it, after saying which item they read and why.
func PromptingInternet(ctx context.Context, server string, wait time.Duration) (Login, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/security", "find-internet-password", "-g", "-s", server) //nolint:gosec // fixed binary; the server comes from TokenOps' own tables
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		switch {
		case ctx.Err() != nil:
			return Login{}, fmt.Errorf("%w: the prompt for %q went unanswered after %s", ErrDenied, server, wait)
		case errors.As(err, &ee) && ee.ExitCode() == securityNotFound:
			return Login{}, ErrNotFound
		case errors.As(err, &ee):
			return Login{}, fmt.Errorf("%w: %q (security exit %d)", ErrDenied, server, ee.ExitCode())
		}
		return Login{}, fmt.Errorf("keychain: read %q: %w", server, err)
	}
	return parseSecurityLogin(stdout.String(), stderr.String())
}

// parseSecurityLogin reads `security find-internet-password -g`: the
// attributes on stdout ("acct"<blob>="4242") and the password on stderr
// (password: "..." or, for bytes that are not text, password: 0x...).
func parseSecurityLogin(stdout, stderr string) (Login, error) {
	var l Login
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, `"acct"<blob>=`); ok {
			l.Account = strings.Trim(rest, `"`)
		}
	}
	for _, line := range strings.Split(stderr, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "password: ")
		if !ok {
			continue
		}
		if s, ok := strings.CutPrefix(rest, `"`); ok {
			l.Secret = strings.TrimSuffix(s, `"`)
		} else if h, ok := strings.CutPrefix(rest, "0x"); ok {
			if b, err := hex.DecodeString(strings.Fields(h)[0]); err == nil {
				l.Secret = string(b)
			}
		}
	}
	if l.Account == "" || l.Secret == "" || l.Account == "<NULL>" {
		return Login{}, ErrNotFound
	}
	return l, nil
}
