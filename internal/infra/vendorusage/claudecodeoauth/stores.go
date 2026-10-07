package claudecodeoauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	oauth "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodeoauth"
)

// FileStore is ~/.claude/.credentials.json: where Claude Code keeps its
// sign-in on Linux and Windows, and on macOS when the Keychain refused the
// write.
type FileStore struct{ Path string }

// Name implements oauth.Store.
func (FileStore) Name() string { return "credentials file" }

// Read implements oauth.Store.
func (f FileStore) Read(context.Context) (oauth.Credentials, error) {
	raw, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return oauth.Credentials{}, oauth.ErrNotSignedIn
	}
	if err != nil {
		return oauth.Credentials{}, fmt.Errorf("claude-code-oauth: %w", err)
	}
	return oauth.Parse(raw)
}

// keychainService is the Keychain item Claude Code writes on macOS.
const keychainService = "Claude Code-credentials"

// KeychainStore reads the macOS Keychain item through /usr/bin/security.
// macOS asks the operator to allow it; Claude Code rewrites the item when
// it renews its token, which can ask again.
type KeychainStore struct {
	// Run executes a command and returns its stdout; nil uses exec.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Name implements oauth.Store.
func (KeychainStore) Name() string { return "macOS Keychain" }

// Prompts implements oauth.PromptingStore: macOS asks the operator.
func (KeychainStore) Prompts() bool { return true }

// keychainTimeout bounds the read: a prompt nobody answers must not hold
// the poller.
const keychainTimeout = 30 * time.Second

// Read implements oauth.Store.
func (k KeychainStore) Read(ctx context.Context) (oauth.Credentials, error) {
	run := k.Run
	if run == nil {
		run = runCommand
	}
	ctx, cancel := context.WithTimeout(ctx, keychainTimeout)
	defer cancel()
	out, err := run(ctx, "/usr/bin/security", "find-generic-password", "-s", keychainService, "-w")
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit) && exit.ExitCode() == 44:
		// errSecItemNotFound: Claude Code has not signed in here.
		return oauth.Credentials{}, oauth.ErrNotSignedIn
	case err != nil:
		// A denial, a cancelled prompt and a timed-out one look alike
		// from here; each means "not now", and none should be retried at
		// once.
		return oauth.Credentials{}, oauth.ErrKeychainDenied
	}
	return oauth.Parse(out)
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output() //nolint:gosec // fixed binary and arguments
}

// Stores are the places to look, file first: it needs no prompt. The
// Keychain is included only when the operator allowed it.
func Stores(home string, keychain bool) []oauth.Store {
	out := []oauth.Store{FileStore{Path: filepath.Join(home, ".claude", ".credentials.json")}}
	if keychain {
		out = append(out, KeychainStore{})
	}
	return out
}

// The stores satisfy the domain's port.
var (
	_ oauth.Store          = FileStore{}
	_ oauth.PromptingStore = KeychainStore{}
)
