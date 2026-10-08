package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/providersetup"
	"go.klarlabs.de/tokenops/internal/infra/applogin"
)

// appLoginEnv is where setup looks for another application's sign-in.
// Tests point it at a temporary home with fixture files.
var appLoginEnv = providersetup.AppLoginEnv

// verifyAppLogin reads a granted sign-in once and the vendor with it.
// Tests replace it so no vendor is called.
var verifyAppLogin = providersetup.VerifyAppLogin

// runAppLoginSetup grants TokenOps one other application's sign-in for a
// provider (ADR 0013). It says exactly what would be read, from where, and
// where the token goes; asks; reads it once against the vendor; and only
// then records the grant. Nothing secret is written: the daemon reads the
// item afresh, read-only, as it polls.
func runAppLoginSetup(cmd *cobra.Command, p providersetup.Provider, opts providerSetupOptions) error {
	out := cmd.OutOrStdout()
	if !p.AppLogin {
		return fmt.Errorf("%s is not read with another application's sign-in; run setup %s without --use-app-login", p.Name, p.ID)
	}
	env := appLoginEnv(keychainDisabled(opts.configPath))
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	a, err := providersetup.FindAppLogin(ctx, p.ID, env)
	if err != nil {
		if errors.Is(err, applogin.ErrNotFound) {
			return fmt.Errorf("no sign-in for %s found on this machine (sign in with its own app or CLI first); nothing was read or written", p.Name)
		}
		return fmt.Errorf("%w; nothing was read or written", err)
	}
	fmt.Fprintf(out, "Connecting %s with %s's own sign-in.\n\n", p.Name, a.App)
	fmt.Fprintf(out, "  What is read:   %s\n", a.What)
	fmt.Fprintf(out, "  Sent only to:   %s\n", a.Host)
	fmt.Fprintln(out, "  Stored:         nothing secret; only this grant, in your config")
	fmt.Fprintln(out, "\nThe daemon re-reads it, read-only, each time it polls, so a token the app renews is picked up.")
	fmt.Fprintln(out, "TokenOps never writes it, refreshes or rotates it, or logs it. Undo with --revoke-app-login.")
	if !confirm(cmd, "\nAllow TokenOps to read it? [y/N] ") {
		return errors.New("not granted; nothing was read or written")
	}
	progress := startActivity(cmd.ErrOrStderr(), "Reading it once and asking "+p.Name)
	lines, err := verifyAppLogin(ctx, p.ID, a, env)
	if err != nil {
		progress.failure(p.Name + " did not accept it")
		if errors.Is(err, providersetup.ErrRefused) {
			return fmt.Errorf("%s refused that sign-in (sign in again with %s). Nothing was written", p.Name, a.App)
		}
		return fmt.Errorf("could not read %s with it: %w. Nothing was written", p.Name, err)
	}
	progress.success(p.Name + " accepted it")
	fmt.Fprintf(out, "\n%s reports:\n", p.Name)
	for _, l := range lines {
		fmt.Fprintln(out, "  "+l)
	}
	path, err := resolveMutableConfigPath(opts.configPath)
	if err != nil {
		return err
	}
	cfg, err := readMutableConfig(path)
	if err != nil {
		return err
	}
	providersetup.ApplyGrant(&cfg, p.ID, a, time.Now())
	if err := writeMutableConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nwrote the grant to %s (vendor_usage.grants.%s)\n", path, p.ID)
	applyRestart(out, opts.restart, false)
	return nil
}

// runAppLoginRevoke removes a provider's grant: the daemon stops reading
// the other application's sign-in.
func runAppLoginRevoke(cmd *cobra.Command, id string, opts providerSetupOptions) error {
	out := cmd.OutOrStdout()
	path, err := resolveMutableConfigPath(opts.configPath)
	if err != nil {
		return err
	}
	cfg, err := readMutableConfig(path)
	if err != nil {
		return err
	}
	if !providersetup.RevokeGrant(&cfg, id) {
		fmt.Fprintf(out, "No sign-in was granted for %s; nothing changed.\n", id)
		return nil
	}
	if err := writeMutableConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "Revoked: TokenOps no longer reads another application's sign-in for %s (%s).\n", id, path)
	applyRestart(out, opts.restart, false)
	return nil
}

// confirm asks a yes/no question; anything but y or yes, or no answer at
// all, is no.
func confirm(cmd *cobra.Command, prompt string) bool {
	line, err := readLine(cmd, prompt)
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
