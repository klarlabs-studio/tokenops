package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"time"
)

// An approval needs a person (ADR 0014): the daemon's API only denies,
// because the agent a boundary stopped can reach it too, and its token is
// readable by any process of the user. So Approve does not call the API.
// It opens a terminal running `tokenops approve <id> --confirm`, which
// shows what is waiting and answers only when the person types yes.

var decisionID = regexp.MustCompile(`^bnd-[0-9a-f]{12}$`)

// errBadDecision is an id that is not a boundary decision's.
var errBadDecision = errors.New("not a boundary decision id")

// errNoTerminal is a platform where the menu bar cannot open a terminal.
var errNoTerminal = errors.New("approve in a terminal: tokenops approve <id> --confirm")

func validDecisionID(id string) bool { return decisionID.MatchString(id) }

// openFile opens path with its default application; a variable so tests
// do not open a terminal.
var openFile = func(path string) error {
	if runtime.GOOS != "darwin" {
		return errNoTerminal
	}
	return exec.Command("open", path).Start() //nolint:gosec // a script this process wrote, id validated
}

// approvalScript is the terminal script for id. A login shell finds
// tokenops on the operator's own PATH; Homebrew's prefixes are added in
// case the shell's profile does not set them.
func approvalScript(id string, session bool) string {
	args := "approve " + id + " --confirm"
	if session {
		args += " --session"
	}
	return "#!/bin/zsh -l\n" +
		"# Opened by the TokenOps menu bar: an approval needs you at a terminal.\n" +
		"export PATH=\"$PATH:/opt/homebrew/bin:/usr/local/bin\"\n" +
		"tokenops " + args + "\n" +
		"echo\n" +
		"echo \"You can close this window.\"\n"
}

// openApproval writes the script for id and opens it in a terminal. The
// id is checked before it reaches the script.
func openApproval(id string, session bool) error {
	if !validDecisionID(id) {
		return errBadDecision
	}
	dir, err := os.MkdirTemp("", "tokenops-approve-")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, id+".command")
	if err := os.WriteFile(path, []byte(approvalScript(id, session)), 0o700); err != nil { //nolint:gosec // must be executable for Terminal to run it
		return err
	}
	if err := openFile(path); err != nil {
		return fmt.Errorf("could not open a terminal: %w", err)
	}
	// The terminal reads the script as it starts; leave it a while, then
	// tidy up.
	time.AfterFunc(time.Minute, func() { _ = os.RemoveAll(dir) })
	return nil
}
