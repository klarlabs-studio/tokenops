package mcp

import "testing"

func TestRegisterFmtTools_NilServer(t *testing.T) {
	if err := RegisterFmtTools(nil, FmtDeps{}); err == nil {
		t.Error("nil server should error")
	}
}

func TestRegisterFmtTools_OK(t *testing.T) {
	srv := NewServer("test", "0.0.0", nil)
	if err := RegisterFmtTools(srv, FmtDeps{}); err != nil {
		t.Fatalf("RegisterFmtTools: %v", err)
	}
}
