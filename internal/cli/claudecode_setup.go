package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/claudesignin"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/version"
)

// claudeCodeBaseURL overrides Anthropic's API in tests.
var claudeCodeBaseURL string

// runClaudeCodeSetup turns on the Claude Code sign-in source: it reads the
// sign-in once, while the operator is here to answer macOS, proves it reads
// usage, and only then writes the switch. The token is never printed and
// never written anywhere.
func runClaudeCodeSetup(cmd *cobra.Command, configPath string, restart, keychain bool) error {
	out := cmd.OutOrStdout()
	keychain = keychain && runtime.GOOS == "darwin"
	if keychain && keychainDisabled(configPath) {
		return errors.New("keychain.disabled is set, so TokenOps does not read the Keychain; unset it to use --keychain. nothing was written")
	}
	fmt.Fprintln(out, "Connecting Claude Code's own sign-in as a source of Claude's plan windows.")
	if keychain {
		fmt.Fprintln(out, "macOS may ask to let tokenops read \"Claude Code-credentials\" from your Keychain: Claude Code's own sign-in,")
		fmt.Fprintln(out, "sent only to api.anthropic.com to read your plan's usage. Choose Always Allow to let the daemon read it too:")
		fmt.Fprintln(out, "the daemon reads it without ever asking, and skips it when macOS would ask (after Claude Code renews its")
		fmt.Fprintln(out, "sign-in, or a TokenOps upgrade), until you run this again.")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), time.Minute)
	defer cancel()
	progress := startActivity(cmd.ErrOrStderr(), "Reading Claude Code's sign-in and asking Anthropic for usage")
	r, err := claudesignin.Check(ctx, claudesignin.Options{
		Home: home, Keychain: keychain, BaseURL: claudeCodeBaseURL, UserAgent: "tokenops/" + version.Version,
	})
	if err != nil {
		progress.failure("not connected")
		if !keychain && runtime.GOOS == "darwin" && errors.Is(err, claudesignin.ErrNotSignedIn) {
			return fmt.Errorf("%w: on macOS Claude Code keeps its sign-in in the Keychain. Run again with --keychain to let TokenOps read it (macOS asks first); nothing was written", err)
		}
		return fmt.Errorf("%w; nothing was written", err)
	}
	progress.success("connected")
	if !r.RateLimitedUntil.IsZero() {
		fmt.Fprintf(out, "\nAnthropic is rate-limiting usage reads until %s; the sign-in itself was read.\n", r.RateLimitedUntil.Local().Format("15:04"))
	} else {
		fmt.Fprintln(out, "\nAnthropic reports:")
		for _, line := range r.Summary {
			fmt.Fprintln(out, "  "+line)
		}
	}
	if r.Plan != "" {
		fmt.Fprintf(out, "Plan: %s\n", r.Plan)
	}
	return enableClaudeCodeSource(out, configPath, restart, keychain)
}

func enableClaudeCodeSource(out io.Writer, configPath string, restart, keychain bool) error {
	path, err := resolveMutableConfigPath(configPath)
	if err != nil {
		return err
	}
	cfg, err := readMutableConfig(path)
	if err != nil {
		return err
	}
	cfg.VendorUsage.ClaudeCodeOAuth.Enabled = true
	cfg.VendorUsage.ClaudeCodeOAuth.Keychain = keychain
	if err := writeMutableConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nwrote %s (vendor_usage.claude_code_oauth)\n", path)
	fmt.Fprintln(out, "The daemon reads it every 10 minutes alongside the status line and the claude.ai meter; the newest reading of each window wins.")
	applyRestart(out, restart, false)
	return nil
}

// keychainDisabled reports whether the operator turned the Keychain off
// (keychain.disabled, or TOKENOPS_KEYCHAIN_DISABLED), in the config at
// configPath.
func keychainDisabled(configPath string) bool {
	if path, err := resolveMutableConfigPath(configPath); err == nil {
		if cfg, err := config.Load(path); err == nil {
			return cfg.Keychain.Disabled
		}
	}
	switch strings.ToLower(os.Getenv("TOKENOPS_KEYCHAIN_DISABLED")) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
