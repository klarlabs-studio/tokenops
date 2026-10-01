package cli

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestMain pins $HOME to a throwaway directory for every test in this
// package.
//
// Several commands here write to the operator's real machine — init
// registers MCP servers in ~/.claude.json, hooks merge into
// ~/.claude/settings.json — and relying on each test to remember to
// sandbox itself does not hold. It already failed once: adding the wiring
// step to init left two tests constructing the command directly, and a
// plain `go test ./internal/cli/` repointed the maintainer's live MCP
// entries at a go-build test binary.
//
// Setting it here makes the isolation structural rather than a convention
// a future test can forget.
func TestMain(m *testing.M) {
	sandbox, err := os.MkdirTemp("", "tokenops-cli-test-home")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create sandbox home: %v\n", err)
		os.Exit(1)
	}
	// Unix uses $HOME; Windows resolves USERPROFILE. The XDG directories
	// win over $HOME where set: with XDG_CONFIG_HOME exported, a sandboxed
	// $HOME still resolved the operator's real config.yaml.
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		os.Setenv(k, sandbox)
	}
	// TOKENOPS_* variables (TOKENOPS_CONFIG among them) point commands at
	// the operator's files and credentials directly.
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "TOKENOPS_") {
			os.Unsetenv(k)
		}
	}

	// Currency detection reads the machine's region; tests see USD unless
	// they say otherwise.
	detectCurrency = func() (string, string) { return "USD", "test default" }

	code := m.Run()
	_ = os.RemoveAll(sandbox)
	os.Exit(code)
}
