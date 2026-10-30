package coachhook

import (
	"fmt"
	"os"
	"testing"
)

// TestMain pins $HOME and the XDG directories to a throwaway sandbox for
// every test in this package. A Cursor turn left without a TurnLedgerDir
// is appended to ~/.tokenops/cursor-turns, and the daemon ingests that
// ledger as real spend: a fixture written there once became thousands of
// phantom grok-4.6 turns in an operator's event store.
func TestMain(m *testing.M) {
	sandbox, err := os.MkdirTemp("", "tokenops-coachhook-test-home")
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
