package fireworks

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/fireworks"
)

// nexusStatusTimeout bounds one `nexus status --json`.
const nexusStatusTimeout = 5 * time.Second

// maxNexusStatusBytes is the most of its output that is read.
const maxNexusStatusBytes = 256 << 10

// NexusStatus asks Nexus CLI who is signed in, with `nexus status --json`,
// which Fireworks documents as machine-readable sign-in and key-storage
// details and which exits non-zero when signed out. It never runs
// `nexus login`, which is interactive and can replace the key. The output
// is parsed for ids and an email and never logged.
type NexusStatus struct {
	// Home is where ~/.nexus is; empty uses $HOME.
	Home string
	// LookPath finds a binary on PATH; nil uses exec.LookPath.
	LookPath func(string) (string, error)
	// Places are checked after PATH; nil uses Homebrew's and /usr/local's.
	Places []string
}

// SignIn is the parsed sign-in, or the zero SignIn when Nexus CLI is not
// installed, is signed out, or says nothing that fits.
func (n NexusStatus) SignIn(ctx context.Context) usage.SignIn {
	bin := n.binary()
	if bin == "" {
		return usage.SignIn{}
	}
	ctx, cancel := context.WithTimeout(ctx, nexusStatusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "status", "--json") //nolint:gosec // Nexus CLI's own binary, fixed arguments, no shell
	cmd.Stdin, cmd.Stderr = nil, nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return usage.SignIn{}
	}
	if err := cmd.Start(); err != nil {
		return usage.SignIn{}
	}
	out, _ := io.ReadAll(io.LimitReader(stdout, maxNexusStatusBytes))
	_, _ = io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		return usage.SignIn{}
	}
	return usage.ParseNexusStatus(out)
}

// binary is Nexus CLI's executable: its own bin directory first, then
// PATH, then the usual Homebrew and /usr/local places (a daemon started
// by launchd has a short PATH). Another tool may be called nexus, so
// nothing is looked for unless Nexus CLI's own ~/.nexus exists.
func (n NexusStatus) binary() string {
	home := n.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if info, err := os.Stat(filepath.Join(home, ".nexus")); err != nil || !info.IsDir() {
		return ""
	}
	look := n.LookPath
	if look == nil {
		look = exec.LookPath
	}
	candidates := []string{filepath.Join(home, ".nexus", "bin", "nexus")}
	if p, err := look("nexus"); err == nil {
		candidates = append(candidates, p)
	}
	places := n.Places
	if places == nil {
		places = []string{"/opt/homebrew/bin/nexus", "/usr/local/bin/nexus"}
	}
	candidates = append(candidates, places...)
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return c
		}
	}
	return ""
}
