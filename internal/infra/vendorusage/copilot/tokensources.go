package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/copilot"
	"go.klarlabs.de/tokenops/internal/infra/keychain"
)

// The Copilot CLI looks for its credential in this order (docs.github.com,
// "Authenticating GitHub Copilot CLI", read 2026-10-27): the environment
// variables COPILOT_GITHUB_TOKEN, GH_TOKEN, GITHUB_TOKEN; its OAuth token
// in the system keychain under the service "copilot-cli" (or, where there
// is no keychain, in plaintext in ~/.copilot/config.json); then
// `gh auth token`. TokenSources reads the same places in the same order.
// No token value is ever logged or put in an error.
//
// Enabling the Copilot reader is the operator's consent to read Copilot's
// own sign-in, the IDE plugins' or the CLI's. The GitHub CLI's token is
// another application's sign-in, with scopes well beyond Copilot, so it is
// asked for only when the operator also set use_gh_cli (ADR 0013: nothing
// another application keeps is read without the operator's say).

// envVars are the Copilot CLI's credential variables, in its order.
var envVars = []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"}

// classicPrefix marks a classic personal access token, which Copilot does
// not accept.
const classicPrefix = "ghp_"

// TokenSources are the Copilot CLI's credential stores, tried after the
// IDE plugins' apps.json. keychainDisabled is keychain.disabled: no
// Keychain read at all. useGH adds `gh auth token`, last.
func TokenSources(home string, keychainDisabled, useGH bool) []usage.TokenSource {
	out := []usage.TokenSource{
		EnvSource{},
		KeychainSource{Disabled: keychainDisabled},
		ConfigFileSource{Path: filepath.Join(home, ".copilot", "config.json")},
	}
	if useGH {
		out = append(out, GHSource{})
	}
	return out
}

// EnvSource reads the Copilot CLI's environment variables.
type EnvSource struct {
	// Getenv replaces os.Getenv (tests).
	Getenv func(string) string
}

// Name implements usage.TokenSource.
func (EnvSource) Name() string {
	return "environment (" + strings.Join(envVars, ", ") + ")"
}

// Token implements usage.TokenSource. A classic token is passed over: it
// is likely set for other tools, and Copilot refuses it.
func (e EnvSource) Token(context.Context) (string, error) {
	get := e.Getenv
	if get == nil {
		get = os.Getenv
	}
	for _, name := range envVars {
		if v := strings.TrimSpace(get(name)); v != "" && !strings.HasPrefix(v, classicPrefix) {
			return v, nil
		}
	}
	return "", usage.ErrNoToken
}

// keychainService is the Keychain item the Copilot CLI keeps its OAuth
// token in.
const keychainService = "copilot-cli"

// KeychainSource reads the Copilot CLI's Keychain item quietly: the daemon
// never shows a Keychain prompt. When macOS would ask, the read fails with
// a reason instead.
type KeychainSource struct {
	// Disabled is keychain.disabled.
	Disabled bool
	// QuietRead replaces keychain.Quiet (tests).
	QuietRead func(keychain.Item) (string, error)
}

// Name implements usage.TokenSource.
func (KeychainSource) Name() string { return "macOS Keychain (" + keychainService + ")" }

// Token implements usage.TokenSource.
func (k KeychainSource) Token(context.Context) (string, error) {
	if k.Disabled {
		return "", usage.ErrNoToken
	}
	read := k.QuietRead
	if read == nil {
		read = keychain.Quiet
	}
	v, err := read(keychain.Item{Service: keychainService})
	switch {
	case errors.Is(err, keychain.ErrNotFound), errors.Is(err, keychain.ErrUnsupported):
		return "", usage.ErrNoToken
	case errors.Is(err, keychain.ErrInteractionRequired):
		return "", errors.New("macOS would ask to allow reading the Copilot CLI's Keychain item, and TokenOps never prompts in the background; sign in to the GitHub CLI (gh auth login) or set COPILOT_GITHUB_TOKEN")
	case err != nil:
		return "", fmt.Errorf("read the Copilot CLI's Keychain item: %w", err)
	}
	if v = strings.TrimSpace(v); v == "" {
		return "", usage.ErrNoToken
	}
	return v, nil
}

// ConfigFileSource reads the Copilot CLI's plaintext fallback,
// ~/.copilot/config.json, which also holds its settings. Its layout is not
// documented, so any value shaped like a GitHub token under a key that
// names a token is taken; a file with none is "no token here".
type ConfigFileSource struct{ Path string }

// Name implements usage.TokenSource.
func (ConfigFileSource) Name() string { return "~/.copilot/config.json" }

// Token implements usage.TokenSource.
func (c ConfigFileSource) Token(context.Context) (string, error) {
	raw, err := os.ReadFile(c.Path)
	if errors.Is(err, os.ErrNotExist) {
		return "", usage.ErrNoToken
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", c.Path, err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("parse %s: %w", c.Path, err)
	}
	if tok := findToken(doc, false); tok != "" {
		return tok, nil
	}
	return "", usage.ErrNoToken
}

// tokenPrefixes are the token kinds Copilot accepts (OAuth, GitHub App
// user-to-server, fine-grained PAT).
var tokenPrefixes = []string{"gho_", "ghu_", "github_pat_"}

// findToken walks doc for a token-shaped string under a key containing
// "token" (or inside such a key's object).
func findToken(doc any, underTokenKey bool) string {
	switch v := doc.(type) {
	case map[string]any:
		for k, child := range v {
			if tok := findToken(child, underTokenKey || strings.Contains(strings.ToLower(k), "token")); tok != "" {
				return tok
			}
		}
	case []any:
		for _, child := range v {
			if tok := findToken(child, underTokenKey); tok != "" {
				return tok
			}
		}
	case string:
		if underTokenKey && hasTokenPrefix(v) {
			return v
		}
	}
	return ""
}

func hasTokenPrefix(s string) bool {
	for _, p := range tokenPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// ghTimeout bounds one `gh auth token`.
const ghTimeout = 10 * time.Second

// GHSource asks the GitHub CLI for its token (`gh auth token`): fixed
// arguments, no shell, no stdin, a deadline.
type GHSource struct {
	// Locate finds gh; nil looks on PATH and where installers put it
	// (launchd's PATH hides Homebrew).
	Locate func() (string, bool)
	// Run executes bin with args and returns stdout; nil uses exec.
	Run func(ctx context.Context, bin string, args ...string) ([]byte, error)
}

// Name implements usage.TokenSource.
func (GHSource) Name() string { return "GitHub CLI (gh auth token)" }

// Token implements usage.TokenSource. gh not installed, or not signed in,
// is "no token here".
func (g GHSource) Token(ctx context.Context) (string, error) {
	locate, run := g.Locate, g.Run
	if locate == nil {
		locate = locateGH
	}
	if run == nil {
		run = runGH
	}
	bin, ok := locate()
	if !ok {
		return "", usage.ErrNoToken
	}
	ctx, cancel := context.WithTimeout(ctx, ghTimeout)
	defer cancel()
	out, err := run(ctx, bin, "auth", "token")
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("gh auth token did not answer within %s", ghTimeout)
		}
		return "", usage.ErrNoToken
	}
	tok := strings.TrimSpace(string(out))
	switch {
	case tok == "":
		return "", usage.ErrNoToken
	case strings.HasPrefix(tok, classicPrefix):
		return "", errors.New("the GitHub CLI is signed in with a classic personal access token, which Copilot does not accept; run gh auth login")
	}
	return tok, nil
}

func locateGH() (string, bool) {
	if p, err := exec.LookPath("gh"); err == nil {
		return p, true
	}
	for _, p := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh"} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

func runGH(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // the operator's own gh, fixed arguments, no shell
	cmd.Stdin = nil
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

// The sources satisfy the domain's port.
var (
	_ usage.TokenSource = EnvSource{}
	_ usage.TokenSource = KeychainSource{}
	_ usage.TokenSource = ConfigFileSource{}
	_ usage.TokenSource = GHSource{}
)
