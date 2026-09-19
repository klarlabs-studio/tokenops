package mcp

import (
	"fmt"
	"os"
	"testing"
)

// TestMain pins $HOME and the XDG directories to a throwaway sandbox for
// every test in this package.
//
// A test here that boots the real daemon (TestE2EDaemonBootHealthShutdown)
// published its URL hint to the operator's real ~/.tokenops/daemon.url and
// deleted it on shutdown. Every `go test ./...` therefore removed the live
// daemon's hint, after which the MCP server reported a healthy daemon as
// absent — and `tokenops_mode` would have started a second one. Sandboxing
// here makes the isolation structural rather than a convention each test
// has to remember; internal/cli learned the same lesson first.
func TestMain(m *testing.M) {
	sandbox, err := os.MkdirTemp("", "tokenops-mcp-test-home")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create sandbox home: %v\n", err)
		os.Exit(1)
	}
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
		os.Setenv(k, sandbox)
	}

	code := m.Run()
	_ = os.RemoveAll(sandbox)
	os.Exit(code)
}
