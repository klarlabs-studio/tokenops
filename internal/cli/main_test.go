package cli

import (
	"fmt"
	"os"
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
	// Unix uses $HOME; Windows resolves USERPROFILE.
	os.Setenv("HOME", sandbox)
	os.Setenv("USERPROFILE", sandbox)

	code := m.Run()
	_ = os.RemoveAll(sandbox)
	os.Exit(code)
}
