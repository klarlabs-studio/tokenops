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
	"go.klarlabs.de/tokenops/internal/infra/keychain"
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

// KeychainStore reads Claude Code's macOS Keychain item. A prompting store
// asks the operator to allow it, through /usr/bin/security; it is what
// `tokenops vendor-usage setup claude-code --keychain` uses, after saying
// so. A quiet store never shows anything: it is what the daemon uses, and
// when macOS would ask, it reports the Keychain as denied for now.
type KeychainStore struct {
	// Quiet reads without any UI.
	Quiet bool
	// Run executes a command and returns its stdout; nil uses exec.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// QuietRead replaces keychain.Quiet (tests).
	QuietRead func(keychain.Item) (string, error)
}

// Name implements oauth.Store.
func (KeychainStore) Name() string { return "macOS Keychain" }

// Prompts implements oauth.PromptingStore: macOS asks the operator, unless
// the store reads quietly.
func (k KeychainStore) Prompts() bool { return !k.Quiet }

// keychainTimeout bounds the prompting read: a prompt nobody answers must
// not hold the command.
const keychainTimeout = 30 * time.Second

// Read implements oauth.Store.
func (k KeychainStore) Read(ctx context.Context) (oauth.Credentials, error) {
	if k.Quiet {
		read := k.QuietRead
		if read == nil {
			read = keychain.Quiet
		}
		v, err := read(keychain.Item{Service: keychainService})
		switch {
		case errors.Is(err, keychain.ErrNotFound):
			return oauth.Credentials{}, oauth.ErrNotSignedIn
		case err != nil:
			// macOS would have asked, or the read failed: "not now".
			return oauth.Credentials{}, oauth.ErrKeychainDenied
		}
		return oauth.Parse([]byte(v))
	}
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
// Keychain is included only when the operator allowed it (useKeychain), and
// read quietly unless prompting is wanted: only a setup command the
// operator ran prompts.
func Stores(home string, useKeychain, prompting bool) []oauth.Store {
	out := []oauth.Store{FileStore{Path: filepath.Join(home, ".claude", ".credentials.json")}}
	if useKeychain {
		out = append(out, KeychainStore{Quiet: !prompting})
	}
	return out
}

// The stores satisfy the domain's port.
var (
	_ oauth.Store          = FileStore{}
	_ oauth.PromptingStore = KeychainStore{}
)
