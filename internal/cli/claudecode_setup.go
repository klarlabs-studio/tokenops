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

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodeoauth"
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
	fmt.Fprintln(out, "Connecting Claude Code's own sign-in as a source of Claude's plan windows.")
	if keychain {
		fmt.Fprintln(out, "macOS may ask to allow reading \"Claude Code-credentials\" from your Keychain. Choose Always Allow to let the daemon read it too;")
		fmt.Fprintln(out, "it may ask again after Claude Code renews its sign-in.")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), time.Minute)
	defer cancel()
	creds, err := claudecodeoauth.ReadFirst(ctx, claudecodeoauth.Stores(home, keychain))
	if err != nil {
		return fmt.Errorf("%w; nothing was written", err)
	}
	if creds.Expired(time.Now()) {
		return fmt.Errorf("%w; nothing was written", claudecodeoauth.ErrExpired)
	}
	client := claudecodeoauth.Client{BaseURL: claudeCodeBaseURL, UserAgent: "tokenops/" + version.Version}
	progress := startActivity(cmd.ErrOrStderr(), "Reading usage from Anthropic")
	usage, err := client.Usage(ctx, creds.AccessToken, time.Now())
	if err != nil {
		progress.failure("Anthropic did not return usage")
	} else {
		progress.success("Anthropic returned usage")
	}
	var limited *claudecodeoauth.RateLimitedError
	switch {
	case errors.As(err, &limited):
		fmt.Fprintf(out, "\nAnthropic is rate-limiting usage reads until %s; the sign-in itself was read.\n", limited.Until.Local().Format("15:04"))
	case err != nil:
		return fmt.Errorf("%w; nothing was written", err)
	case !usage.HasSignal():
		return errors.New("signed in to Claude Code, but Anthropic reports no plan windows for it; nothing was written")
	default:
		fmt.Fprintln(out, "\nAnthropic reports:")
		for _, line := range usage.Summary() {
			fmt.Fprintln(out, "  "+line)
		}
	}
	if creds.SubscriptionType != "" {
		fmt.Fprintf(out, "Plan: %s\n", strings.ToUpper(creds.SubscriptionType[:1])+creds.SubscriptionType[1:])
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
