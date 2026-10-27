package fireworks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/fireworks"
)

func script(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { //nolint:gosec // test fixture script
		t.Fatal(err)
	}
}

func noPath(string) (string, error) { return "", errors.New("not found") }

// `nexus status --json` is run without a shell from Nexus CLI's own bin
// directory, and its sign-in is parsed; a signed-out exit is nothing.
func TestNexusStatusSignIn(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".nexus", "bin", "nexus")
	script(t, bin, `[ "$1 $2" = "status --json" ] || exit 2
echo '{"signedIn":true,"user":"accounts/acme/users/u-ada","email":"ada@example.com"}'`)
	got := NexusStatus{Home: home, LookPath: noPath, Places: []string{}}.SignIn(context.Background())
	if got != (usage.SignIn{Account: "acme", User: "u-ada", Email: "ada@example.com"}) {
		t.Errorf("got %+v", got)
	}
	script(t, bin, `echo '{"signedIn":false,"email":"ada@example.com"}'; exit 1`)
	if got := (NexusStatus{Home: home, LookPath: noPath, Places: []string{}}).SignIn(context.Background()); got != (usage.SignIn{}) {
		t.Errorf("signed out: %+v", got)
	}
}

// Without Nexus CLI there is nothing to run; on PATH it is found there.
func TestNexusStatusFindsTheBinary(t *testing.T) {
	home := t.TempDir()
	if got := (NexusStatus{Home: home, LookPath: noPath, Places: []string{}}).SignIn(context.Background()); got != (usage.SignIn{}) {
		t.Errorf("no binary: %+v", got)
	}
	onPath := filepath.Join(t.TempDir(), "nexus")
	script(t, onPath, `echo '{"email":"ada@example.com"}'`)
	look := func(name string) (string, error) {
		if name == "nexus" {
			return onPath, nil
		}
		return "", errors.New("not found")
	}
	if got := (NexusStatus{Home: home, LookPath: look, Places: []string{}}).SignIn(context.Background()); got.Email != "ada@example.com" {
		t.Errorf("on PATH: %+v", got)
	}
}
