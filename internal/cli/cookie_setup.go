package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
)

// newVendorUsageSetupCmd walks an operator through wiring the claude.ai
// cookie poller, and proves it works before writing anything.
//
// `vendor-usage enable claude-usage-meter --session-key ...` already existed
// and is the wrong shape for this particular source. It writes the key and
// reports success without ever contacting Anthropic, so a mistyped or
// expired cookie surfaces as nothing at all — the poller simply logs
// "Usage() failed" on every tick into a file nobody reads. One machine
// accumulated 3,019 of those.
//
// It is also the only source whose credential has to be fished out of
// browser devtools, which is four clicks the help text can describe and a
// flag cannot.
func newVendorUsageSetupCmd() *cobra.Command {
	var (
		configPath    string
		noRestartFlag bool
	)
	cmd := &cobra.Command{
		Use:   "setup claude-usage-meter",
		Short: "Walk through connecting claude.ai's own usage meter, and verify it",
		Long: `setup connects the claude.ai session cookie that carries Anthropic's own
utilisation percentages — the 5-hour and 7-day windows shown in the app.

It is the only authoritative reading TokenOps can get for a Claude
subscription: everything else is estimated from message counts against a
published cap. The cookie is read locally, sent only to claude.ai, and
stored in your config file.

The session key is verified against Anthropic before anything is written,
so a mistyped or expired cookie fails here rather than silently producing
no data.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && !strings.EqualFold(args[0], "claude-usage-meter") {
				return fmt.Errorf("setup currently covers claude-usage-meter only; got %q", args[0])
			}
			return runCookieSetup(cmd, configPath, !noRestartFlag)
		},
	}
	cmd.Flags().StringVar(&configPath, "config-path", "", "override config file path")
	addNoRestartFlag(cmd, &noRestartFlag)
	return cmd
}

func runCookieSetup(cmd *cobra.Command, configPath string, restart bool) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Connecting claude.ai's usage meter.")
	fmt.Fprintln(out, "\nTokenOps needs the session cookie your browser already holds:")
	fmt.Fprintln(out, "  1. open https://claude.ai and sign in")
	fmt.Fprintln(out, "  2. open developer tools (⌥⌘I on macOS, F12 elsewhere)")
	fmt.Fprintln(out, "  3. Application → Storage → Cookies → https://claude.ai")
	fmt.Fprintln(out, "  4. copy the value of the `sessionKey` row (it starts sk-ant-sid...)")
	fmt.Fprintln(out, "\nIt is sent only to claude.ai, and stored in your local config.")

	key, err := readSecret(cmd, "\nPaste sessionKey: ")
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("no session key entered; nothing was written")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	client := claudeusagemeter.NewClient(key)

	fmt.Fprintln(out, "\nChecking it with Anthropic...")
	orgs, err := client.Organizations(ctx)
	if err != nil {
		return fmt.Errorf("that key was not accepted: %w\n  "+
			"session cookies rotate — if you copied it a while ago, copy it again. "+
			"Nothing was written", err)
	}
	if len(orgs) == 0 {
		return errors.New("the key worked but the account has no organizations; nothing was written")
	}
	org := orgs[0]
	if len(orgs) > 1 {
		if org, err = chooseOrg(cmd, orgs); err != nil {
			return err
		}
	}

	usage, err := client.Usage(ctx, org.UUID)
	if err != nil {
		return fmt.Errorf("could not read usage for %q: %w\n  nothing was written", org.Name, err)
	}

	if !usage.HasSignal() {
		return fmt.Errorf("the key works, but Anthropic reports no usage limits for %q — nothing to meter there. "+
			"If you belong to another organization, run setup again and choose it. Nothing was written", org.Name)
	}
	fmt.Fprintf(out, "\nConnected to %s. Anthropic reports:\n", org.Name)
	for _, line := range usage.Summary() {
		fmt.Fprintln(out, "  "+line)
	}
	if len(usage.Unrecognised) > 0 {
		fmt.Fprintf(out, "  (also returned, in a shape this version cannot read: %s — skipped, not shown as zero)\n",
			strings.Join(usage.Unrecognised, ", "))
	}

	path, err := resolveMutableConfigPath(configPath)
	if err != nil {
		return err
	}
	cfg, err := readMutableConfig(path)
	if err != nil {
		return err
	}
	cfg.VendorUsage.ClaudeUsageMeter.Enabled = true
	cfg.VendorUsage.ClaudeUsageMeter.SessionKey = key
	cfg.VendorUsage.ClaudeUsageMeter.OrgID = org.UUID
	if err := writeMutableConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nwrote %s\n", path)
	fmt.Fprintln(out, "These readings now replace the estimated window in `plan headroom` and the coach.")
	applyRestart(out, restart, true)
	return nil
}

// chooseOrg asks which organization to meter when the account has several.
// Picking silently would meter the wrong workspace on exactly the accounts
// where that matters — someone in more than one org.
func chooseOrg(cmd *cobra.Command, orgs []claudeusagemeter.OrgEntry) (claudeusagemeter.OrgEntry, error) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "\nThis account belongs to several organizations:")
	for i, o := range orgs {
		fmt.Fprintf(out, "  %d) %s\n", i+1, o.Name)
	}
	line, err := readLine(cmd, fmt.Sprintf("Which one? [1-%d]: ", len(orgs)))
	if err != nil {
		return claudeusagemeter.OrgEntry{}, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(orgs) {
		return claudeusagemeter.OrgEntry{}, fmt.Errorf("not a choice between 1 and %d; nothing was written", len(orgs))
	}
	return orgs[n-1], nil
}

// readSecret reads a credential without echoing it.
//
// Without this the key lands in the terminal scrollback of whoever is
// watching, which is the same exposure as putting it in a flag and the
// reason this command exists rather than another flag.
//
// A non-terminal stdin (a pipe, CI) falls back to a plain read: there is no
// echo to suppress, and refusing would break scripted setup.
func readSecret(cmd *cobra.Command, prompt string) (string, error) {
	fmt.Fprint(cmd.OutOrStdout(), prompt)
	in, ok := cmd.InOrStdin().(*os.File)
	if ok && term.IsTerminal(int(in.Fd())) {
		b, err := term.ReadPassword(int(in.Fd()))
		fmt.Fprintln(cmd.OutOrStdout())
		if err != nil {
			return "", fmt.Errorf("read session key: %w", err)
		}
		return string(b), nil
	}
	return readRest(cmd.InOrStdin())
}

func readLine(cmd *cobra.Command, prompt string) (string, error) {
	fmt.Fprint(cmd.OutOrStdout(), prompt)
	return readRest(cmd.InOrStdin())
}

func readRest(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read input: %w", err)
	}
	return line, nil
}
