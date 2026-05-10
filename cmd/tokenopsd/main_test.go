package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	err := run([]string{"version"})
	if err != nil {
		t.Errorf("run(version) = %v, want nil", err)
	}
}

func TestRunHelp(t *testing.T) {
	err := run([]string{"help"})
	if err != nil {
		t.Errorf("run(help) = %v, want nil", err)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	err := run([]string{"nope"})
	if err == nil {
		t.Fatal("run(nope) = nil, want error")
	}
	if !strings.Contains(err.Error(), `unknown command "nope"`) {
		t.Errorf("error = %q, want unknown command", err)
	}
}

func TestRunStartConfigError(t *testing.T) {
	dir := t.TempDir()
	err := run([]string{"start", "--config", filepath.Join(dir, "nope.yaml")})
	if err == nil {
		t.Fatal("run(start --config nope.yaml) = nil, want error")
	}
	if !strings.Contains(err.Error(), "config:") {
		t.Errorf("error = %q, want config: prefix", err)
	}
}
