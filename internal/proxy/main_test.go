package proxy

import (
	"fmt"
	"os"
	"testing"
)

// TestMain pins $HOME and the XDG directories to a throwaway sandbox for
// every test in this package. Routes such as /api/routing/proposals read
// files under ~/.tokenops by default, and a test must never read or create
// the operator's own (internal/mcp and internal/cli learned this first).
func TestMain(m *testing.M) {
	sandbox, err := os.MkdirTemp("", "tokenops-proxy-test-home")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create sandbox home: %v\n", err)
		os.Exit(1)
	}
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
		_ = os.Setenv(k, sandbox)
	}
	code := m.Run()
	_ = os.RemoveAll(sandbox)
	os.Exit(code)
}
