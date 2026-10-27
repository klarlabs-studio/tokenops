package copilot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/copilot"
	"go.klarlabs.de/tokenops/internal/infra/keychain"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// The Copilot CLI's environment variables, in its own order.
func TestEnvSourceFollowsTheCopilotCLIOrder(t *testing.T) {
	ctx := context.Background()
	src := EnvSource{Getenv: envOf(map[string]string{"GH_TOKEN": "gho_fixturegh", "GITHUB_TOKEN": "gho_fixturegithub"})}
	if tok, err := src.Token(ctx); err != nil || tok != "gho_fixturegh" {
		t.Errorf("got %q, %v; want GH_TOKEN before GITHUB_TOKEN", tok, err)
	}
	src = EnvSource{Getenv: envOf(map[string]string{"COPILOT_GITHUB_TOKEN": "gho_fixturecopilot", "GH_TOKEN": "gho_fixturegh"})}
	if tok, _ := src.Token(ctx); tok != "gho_fixturecopilot" {
		t.Errorf("got %q; want COPILOT_GITHUB_TOKEN first", tok)
	}
	if _, err := (EnvSource{Getenv: envOf(nil)}).Token(ctx); !errors.Is(err, usage.ErrNoToken) {
		t.Errorf("no variables: err = %v, want ErrNoToken", err)
	}
}

// Copilot does not accept classic personal access tokens: a GH_TOKEN set
// for other tools is passed over rather than sent and refused.
func TestEnvSourceSkipsClassicTokens(t *testing.T) {
	src := EnvSource{Getenv: envOf(map[string]string{"GH_TOKEN": "ghp_fixtureclassic", "GITHUB_TOKEN": "github_pat_fixturefine"})}
	if tok, err := src.Token(context.Background()); err != nil || tok != "github_pat_fixturefine" {
		t.Errorf("got %q, %v; want the fine-grained token", tok, err)
	}
}

func TestKeychainSourceReadsQuietly(t *testing.T) {
	var asked keychain.Item
	src := KeychainSource{QuietRead: func(it keychain.Item) (string, error) { asked = it; return "gho_fixturekeychain\n", nil }}
	tok, err := src.Token(context.Background())
	if err != nil || tok != "gho_fixturekeychain" {
		t.Errorf("got %q, %v", tok, err)
	}
	if asked.Service != "copilot-cli" {
		t.Errorf("read service %q, want copilot-cli", asked.Service)
	}
}

func TestKeychainSourceErrors(t *testing.T) {
	ctx := context.Background()
	missing := KeychainSource{QuietRead: func(keychain.Item) (string, error) { return "", keychain.ErrNotFound }}
	if _, err := missing.Token(ctx); !errors.Is(err, usage.ErrNoToken) {
		t.Errorf("missing item: err = %v, want ErrNoToken", err)
	}
	prompt := KeychainSource{QuietRead: func(it keychain.Item) (string, error) {
		return "", errors.Join(keychain.ErrInteractionRequired, errors.New(it.String()))
	}}
	_, err := prompt.Token(ctx)
	if err == nil || errors.Is(err, usage.ErrNoToken) || !strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("would prompt: err = %v; want a reason with a way forward", err)
	}
	if _, err := (KeychainSource{Disabled: true, QuietRead: func(keychain.Item) (string, error) {
		t.Error("a disabled Keychain was read")
		return "", nil
	}}).Token(ctx); !errors.Is(err, usage.ErrNoToken) {
		t.Errorf("disabled: err = %v, want ErrNoToken", err)
	}
}

// The plaintext fallback's layout is not documented: any token-shaped
// value under a key naming a token is taken; a settings file without one
// is "no token here".
func TestConfigFileSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	ctx := context.Background()
	if _, err := (ConfigFileSource{Path: path}).Token(ctx); !errors.Is(err, usage.ErrNoToken) {
		t.Errorf("missing file: err = %v, want ErrNoToken", err)
	}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"model":"auto","theme":"dark"}`)
	if _, err := (ConfigFileSource{Path: path}).Token(ctx); !errors.Is(err, usage.ErrNoToken) {
		t.Errorf("settings only: err = %v, want ErrNoToken", err)
	}
	write(`{"model":"auto","copilot_tokens":{"https://github.com:octo":"gho_fixtureplain"}}`)
	if tok, err := (ConfigFileSource{Path: path}).Token(ctx); err != nil || tok != "gho_fixtureplain" {
		t.Errorf("got %q, %v; want the stored token", tok, err)
	}
	write(`{"description":"gho_notatoken_key"}`)
	if _, err := (ConfigFileSource{Path: path}).Token(ctx); !errors.Is(err, usage.ErrNoToken) {
		t.Errorf("token-shaped value under a non-token key: err = %v, want ErrNoToken", err)
	}
	write(`{not json`)
	if _, err := (ConfigFileSource{Path: path}).Token(ctx); err == nil || errors.Is(err, usage.ErrNoToken) {
		t.Errorf("unparsable: err = %v, want a parse error", err)
	}
}

func TestGHSource(t *testing.T) {
	ctx := context.Background()
	var gotBin string
	var gotArgs []string
	var gotTimeout time.Duration
	src := GHSource{
		Locate: func() (string, bool) { return "/fixture/bin/gh", true },
		Run: func(ctx context.Context, bin string, args ...string) ([]byte, error) {
			gotBin, gotArgs = bin, args
			if dl, ok := ctx.Deadline(); ok {
				gotTimeout = time.Until(dl)
			}
			return []byte("gho_fixturegh\n"), nil
		},
	}
	tok, err := src.Token(ctx)
	if err != nil || tok != "gho_fixturegh" {
		t.Fatalf("got %q, %v", tok, err)
	}
	if gotBin != "/fixture/bin/gh" || strings.Join(gotArgs, " ") != "auth token" {
		t.Errorf("ran %s %v; want gh auth token", gotBin, gotArgs)
	}
	if gotTimeout <= 0 || gotTimeout > ghTimeout {
		t.Errorf("deadline %v; want a bounded run", gotTimeout)
	}

	notInstalled := GHSource{Locate: func() (string, bool) { return "", false }}
	if _, err := notInstalled.Token(ctx); !errors.Is(err, usage.ErrNoToken) {
		t.Errorf("no gh: err = %v, want ErrNoToken", err)
	}
	signedOut := GHSource{
		Locate: func() (string, bool) { return "/fixture/bin/gh", true },
		Run:    func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("exit status 1") },
	}
	if _, err := signedOut.Token(ctx); !errors.Is(err, usage.ErrNoToken) {
		t.Errorf("gh not signed in: err = %v, want ErrNoToken", err)
	}
	classic := GHSource{
		Locate: func() (string, bool) { return "/fixture/bin/gh", true },
		Run:    func(context.Context, string, ...string) ([]byte, error) { return []byte("ghp_fixtureclassic\n"), nil },
	}
	if _, err := classic.Token(ctx); err == nil || errors.Is(err, usage.ErrNoToken) {
		t.Errorf("classic token from gh: err = %v; want a reason (Copilot refuses ghp_)", err)
	}
}

// The token never appears in an error.
func TestErrorsNeverCarryTheToken(t *testing.T) {
	src := GHSource{
		Locate: func() (string, bool) { return "/fixture/bin/gh", true },
		Run:    func(context.Context, string, ...string) ([]byte, error) { return []byte("ghp_fixturesecret\n"), nil },
	}
	if _, err := src.Token(context.Background()); err != nil && strings.Contains(err.Error(), "fixturesecret") {
		t.Errorf("error %q carries the token", err)
	}
}

func names(sources []usage.TokenSource) string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		out = append(out, s.Name())
	}
	return strings.Join(out, ", ")
}

// The Copilot CLI's order; the GitHub CLI only when the operator said so.
func TestTokenSourcesOrder(t *testing.T) {
	want := "environment (COPILOT_GITHUB_TOKEN, GH_TOKEN, GITHUB_TOKEN), macOS Keychain (copilot-cli), ~/.copilot/config.json"
	if got := names(TokenSources("/fixture/home", false, false)); got != want {
		t.Errorf("sources = %s\nwant      %s", got, want)
	}
	if got := names(TokenSources("/fixture/home", false, true)); got != want+", GitHub CLI (gh auth token)" {
		t.Errorf("with use_gh_cli: %s", got)
	}
}
