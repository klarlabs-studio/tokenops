package cli

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// newDashboardCmd assembles the `tokenops dashboard` command tree.
// One subcommand for now (rotate-token); leaves a tree so future
// dashboard-management verbs (revoke, list-sessions) plug in without
// re-organising.
func newDashboardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Manage the local dashboard (auth, sessions)",
	}
	cmd.AddCommand(newDashboardRotateTokenCmd())
	return cmd
}

// newDashboardRotateTokenCmd mints a fresh dashboard auth token and
// atomically writes it to ~/.tokenops/dashboard.token (or the XDG
// override). The daemon reads the file on Start so the operator must
// restart the daemon for the new token to take effect; the command
// prints that next-step explicitly so there's no surprise.
//
// Use case: operator shared a dashboard URL with a colleague + wants
// to revoke. Rotate the token + restart the daemon; old URLs return
// 401 against the new daemon.
func newDashboardRotateTokenCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "rotate-token",
		Short: "Mint a fresh dashboard auth token and persist it",
		Long: `rotate-token replaces the value at ~/.tokenops/dashboard.token
(or $XDG_DATA_HOME/tokenops/dashboard.token) with a freshly minted
32-byte random secret. The daemon reads this file once on Start, so
you must restart the daemon for the new token to take effect — the
output reminds you of that next step.

When the dashboard config.admin_token is set explicitly, this command
fails: the config value takes precedence and rotating the file would
have no effect.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(&rootFlags{})
			if err == nil && cfg.Dashboard.AdminToken != "" {
				return fmt.Errorf("config.dashboard.admin_token is set explicitly; remove it or unset to rotate via this command")
			}
			path, err := dashTokenPathCLI()
			if err != nil {
				return err
			}
			tok, err := mintDashTokenCLI()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("ensure parent dir: %w", err)
			}
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, []byte(tok), 0o600); err != nil {
				return fmt.Errorf("write token: %w", err)
			}
			if err := os.Rename(tmp, path); err != nil {
				return fmt.Errorf("atomic rename: %w", err)
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{
					"path":  path,
					"token": tok,
					"hint":  "restart the daemon for the new token to take effect",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rotated dashboard token at %s\n", path)
			fmt.Fprintln(cmd.OutOrStdout(), "next: restart the daemon (Ctrl-C + `tokenops start`) for the new token to take effect")
			fmt.Fprintln(cmd.OutOrStdout(), "old URLs with the previous ?token=… will return 401 after restart")
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the new token + path as JSON")
	return cmd
}

// dashTokenPathCLI mirrors internal/daemon.dashTokenPath so the CLI
// resolves the same location the daemon writes/reads. Kept here
// (rather than imported) because internal/cli must not depend on
// internal/daemon — daemon imports CLI helpers for its serve flow.
func dashTokenPathCLI() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "tokenops", "dashboard.token"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".tokenops", "dashboard.token"), nil
}

// mintDashTokenCLI returns a fresh 32-byte URL-safe token, mirroring
// the daemon's loadOrMintDashToken minter so a token rotated via the
// CLI is indistinguishable from one minted on first daemon start.
func mintDashTokenCLI() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mint token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
