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
	// keychainPath confines a quiet read to one keychain file (tests).
	keychainPath string
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
