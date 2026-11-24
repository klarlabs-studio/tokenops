package cli

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/actions"
)

// planSourceProvider maps each `vendor-usage enable` source that reads a
// plan to its provider: switching one off dismisses the provider, so
// TokenOps no longer suggests connecting it.
var planSourceProvider = map[string]string{
	"claude-subscription": "anthropic",
	"claude-usage-meter":  "anthropic",
	"anthropic-admin":     "anthropic",
	"cursor":              "cursor",
	"github-copilot":      "github",
	"fireworks":           "fireworks",
}

func newVendorUsageDismissCmd() *cobra.Command {
	var undo bool
	cmd := &cobra.Command{
		Use:   "dismiss <provider>",
		Short: "Stop suggesting to connect a tool's plan TokenOps found in use",
		Long: `When TokenOps sees you use a tool, or finds it installed, whose plan it
cannot read yet, glance, status, the menu bar and the agent tools say so
once, in place, with the one command that connects it. dismiss says you
do not want that plan read: it is no longer suggested anywhere
(vendor_usage.dismissed). --undo suggests it again; connecting the plan
takes it off the list as well.

The provider is its ID as glance and status name it: cursor, anthropic,
github, openrouter, …`,
		Example: `  tokenops vendor-usage dismiss cursor
  tokenops vendor-usage dismiss cursor --undo`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := resolveMutableConfigPath(configFlag(cmd))
			if err != nil {
				return err
			}
			change, err := actions.DismissConnection(path, args[0], undo)
			if err != nil {
				return err
			}
			if undo {
				fmt.Fprintf(cmd.OutOrStdout(), "%s is suggested again when it is in use and its plan is not read\nwrote %s\n", change.Provider, path)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is no longer suggested; `tokenops vendor-usage dismiss %s --undo` takes that back\nwrote %s\n",
				change.Provider, change.Provider, path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&undo, "undo", false, "suggest it again")
	return cmd
}

// cursorCookieValue is the WorkosCursorSessionToken cookie's value, from
// the value alone or a pasted "WorkosCursorSessionToken=…" pair.
func cursorCookieValue(raw string) string {
	v := strings.TrimSpace(raw)
	if rest, ok := strings.CutPrefix(v, "WorkosCursorSessionToken="); ok {
		v = rest
	}
	if i := strings.IndexByte(v, ';'); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// cursorUserFromCookie is the user ID a WorkosCursorSessionToken starts
// with ("user_…::<token>", the colons URL-encoded in the browser), so the
// operator need not find it separately; empty when it does not.
func cursorUserFromCookie(cookie string) string {
	v := cookie
	if dec, err := url.QueryUnescape(cookie); err == nil {
		v = dec
	}
	user, _, ok := strings.Cut(v, "::")
	if !ok || !strings.HasPrefix(user, "user_") || strings.ContainsAny(user, " /;") {
		return ""
	}
	return user
}
