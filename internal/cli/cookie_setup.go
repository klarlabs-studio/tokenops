package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"go.klarlabs.de/tokenops/internal/capability/usagemeter"
	"go.klarlabs.de/tokenops/internal/infra/browsercookie"
)

// meterBaseURL overrides claude.ai's address for the verification request.
// Empty in production, which leaves the client on https://claude.ai.
//
// It exists because the refusal tests used to reach the live internet: they
// sent a bogus key to claude.ai and asserted on whatever came back, so they
// passed from a laptop, where Anthropic answers 401, and failed from CI,
// where Cloudflare answers its bot check first. A test of how this command
// reports a refusal should not depend on which IP runs it.
var meterBaseURL string

// newVendorUsageSetupCmd walks an operator through wiring the claude.ai
// cookie poller, and proves it works before writing anything.
//
// `vendor-usage enable claude-subscription --session-key ...` already existed
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
		noRestartFlag bool
		browser       string
		paste         bool
		pasteRequest  bool
		org           string
		noKeychain    bool
	)
	cmd := &cobra.Command{
		Use:   "setup claude-subscription|claude-code",
		Short: "Connect a source of Claude's plan windows, and verify it",
		Long: `setup connects the claude.ai session cookie that carries Anthropic's own
utilisation percentages — the 5-hour and 7-day windows shown in the app.

It is the only authoritative reading TokenOps can get for a Claude
subscription: everything else is estimated from message counts against a
published cap. The cookie is read locally, sent only to claude.ai, and
stored in your config file.

The session key is verified against Anthropic before anything is written,
so a mistyped or expired cookie fails here rather than silently producing
no data. After setup, TokenOps restarts its supervised daemon automatically;
the MCP server observes the updated config without a client restart.

setup claude-code reads the windows with Claude Code's own sign-in instead
(opt-in, ADR 0011): the token Claude Code keeps in ~/.claude/.credentials.json
or, on macOS, the Keychain, which asks you to allow it. The token is held
in memory only, sent only to api.anthropic.com, and never refreshed by
TokenOps; Claude Code renews it when it runs. Nothing is written to your
config but the switch.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && strings.EqualFold(args[0], "claude-code") {
				return runClaudeCodeSetup(cmd, configFlag(cmd), !noRestartFlag, !noKeychain)
			}
			if len(args) == 1 && !isClaudeSubscriptionSource(args[0]) {
				return fmt.Errorf("setup covers claude-subscription and claude-code; got %q", args[0])
			}
			return runCookieSetup(cmd, cookieSetupOptions{
				configPath:   configFlag(cmd),
				restart:      !noRestartFlag,
				browser:      browser,
				paste:        paste,
				pasteRequest: pasteRequest,
				org:          org,
			})
		},
	}
	cmd.Flags().StringVar(&browser, "browser", "",
		"read the cookie from this browser instead of searching ("+strings.Join(browsercookie.Names(), ", ")+")")
	cmd.Flags().BoolVar(&paste, "paste", false,
		"skip the browser and paste the session key at a prompt")
	cmd.Flags().BoolVar(&pasteRequest, "paste-request", false,
		"paste a copied claude.ai usage request instead of reading browser storage")
	cmd.MarkFlagsMutuallyExclusive("paste", "paste-request")
	cmd.Flags().StringVar(&org, "org", "",
		"organization to meter, by name or id (default: the one reporting usage)")
	cmd.Flags().BoolVar(&noKeychain, "no-keychain", false,
		"claude-code: read only ~/.claude/.credentials.json, never the macOS Keychain")
	addNoRestartFlag(cmd, &noRestartFlag)
	return cmd
}

// cookieSetupOptions are the choices setup was given.
type cookieSetupOptions struct {
	configPath string
	restart    bool
	// browser limits the search to one browser; empty searches all.
	browser string
	// paste skips the browser entirely and asks for the key.
	paste bool
	// pasteRequest imports only bounded authentication metadata from a
	// content-free /usage request copied from browser developer tools.
	pasteRequest bool
	// org names the organization to meter; empty picks the one that
	// reports usage.
	org string
}

func runCookieSetup(cmd *cobra.Command, opts cookieSetupOptions) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Connecting claude.ai's usage meter.")

	session, err := cookieSetupKey(cmd, opts)
	if err != nil {
		return err
	}
	key := session.key
	if key == "" {
		return errors.New("no session key entered; nothing was written")
	}
	if session.browser != "" {
		fmt.Fprintf(out, "\nRead the claude.ai session from %s. It is sent only to claude.ai, and stored in your local config.\n", session.browser)
		if session.clearance == "" {
			fmt.Fprintln(out, "  (no cf_clearance cookie there yet — if Anthropic's bot check refuses, open claude.ai in that browser once and run this again)")
		}
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	fmt.Fprintln(out)
	progress := startActivity(cmd.ErrOrStderr(), "Checking credentials with Anthropic")
	// Pick the organization that reports usage rather than asking. An
	// account holds several — a personal one, a Console API one, an
	// Enterprise one — and only some carry a usage meter at all, so the
	// question put a choice to the operator that the data already answers.
	conn, err := usagemeter.Verify(ctx, session.meterSession(), opts.org, meterBaseURL)
	if err != nil {
		progress.failure("Anthropic verification failed")
	} else {
		progress.success("Anthropic credentials verified")
	}
	switch {
	case errors.Is(err, usagemeter.ErrBotCheck):
		return botCheckAdvice(session.browser)
	case errors.Is(err, usagemeter.ErrNothingToMeter):
		return fmt.Errorf("%w — none of this account's organizations reports usage limits. "+
			"Nothing was written", err)
	case err != nil:
		return fmt.Errorf("that key was not accepted: %w\n  "+
			"session cookies rotate — if the browser session is old, sign in to claude.ai again. "+
			"Nothing was written", err)
	}
	org, usage := conn.Org, conn.Usage
	if len(conn.Orgs) > 1 {
		fmt.Fprintf(out, "This account has %d organizations; %q is the one reporting usage (--org picks another).\n",
			len(conn.Orgs), org.Name)
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

	path, err := resolveMutableConfigPath(opts.configPath)
	if err != nil {
		return err
	}
	cfg, err := readMutableConfig(path)
	if err != nil {
		return err
	}
	usagemeter.Apply(&cfg, session.meterSession(), conn)
	if err := writeMutableConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nwrote %s\n", path)
	fmt.Fprintln(out, "These readings now replace the estimated window in `plan headroom` and the coach.")
	if session.browser != "" {
		fmt.Fprintf(out, "The daemon will keep reading the session from %s as it polls; macOS asks to allow that once per installed version.\n", session.browser)
	}
	// serve watches config.yaml and swaps its snapshot in-process, so only the
	// ingestion daemon needs a restart. Asking the operator to restart their
	// MCP host here was both unnecessary and disruptive.
	applyRestart(out, opts.restart, false)
	return nil
}

// chooseOrg asks which organization to meter when the account has several.
// Picking silently would meter the wrong workspace on exactly the accounts

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

func readRest(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read input: %w", err)
	}
	return line, nil
}

// browserSession is what the browser gave us: the session, the clearance
// cookie that proves it passed claude.ai's bot check, and the agent string
// that cookie is bound to.
// meterSession is s as the usage-meter capability takes it.
func (s browserSession) meterSession() usagemeter.Session {
	return usagemeter.Session{
		Key: s.key, Clearance: s.clearance, UserAgent: s.userAgent,
		BrowserHeaders: s.browserHeaders, BrowserCookies: s.browserCookies,
		Browser: s.browser, OrgID: s.orgID,
	}
}

// keychainNotice says, before macOS asks, which Keychain item the browser
// read opens and what for, so the prompt is one the operator expected.
const keychainNotice = `macOS may ask to let tokenops read your browser's "Safe Storage" item from the Keychain
("Chrome Safe Storage", "Arc Safe Storage", ...): it is the key the browser encrypts its cookies
with. TokenOps uses it to read two claude.ai cookies, sessionKey and cf_clearance, and nothing else.
The daemon reads them again when claude.ai renews the session, and macOS may ask again after each
TokenOps upgrade. --paste skips the browser and the Keychain.`

type browserSession struct {
	key            string
	clearance      string
	userAgent      string
	browserHeaders map[string]string
	browserCookies map[string]string
	browser        string
	orgID          string
}

// cookieSetupKey gets the session key without making the operator handle it
// where that is possible: the browser they are signed in with already holds
// it. macOS asks them to allow the keychain read; that prompt is the
// consent, and it replaces finding a value in devtools and pasting it into
// a terminal. Returns the key and where it came from ("" when pasted).
func cookieSetupKey(cmd *cobra.Command, opts cookieSetupOptions) (browserSession, error) {
	out := cmd.OutOrStdout()
	if opts.pasteRequest {
		fmt.Fprintln(out, requestImportInstructions)
		raw, err := readCopiedRequest(cmd, "\nPaste copied cURL request: ")
		if err != nil {
			return browserSession{}, err
		}
		return parseUsageRequest(raw)
	}
	if !opts.paste {
		if _, err := os.UserHomeDir(); err == nil {
			fmt.Fprintln(out, "\nLooking for your claude.ai session in a local browser.")
			fmt.Fprintln(out, keychainNotice)
			// An operator is watching this one: give them time to find the
			// dialog macOS puts up, rather than falling to the paste path
			// while they are still looking for it.
			s, err := usagemeter.FromBrowser(cmd.Context(), opts.browser, browsercookie.InteractiveKeychainWait)
			switch {
			case err == nil:
				return browserSession{key: s.Key, clearance: s.Clearance, userAgent: s.UserAgent, browser: s.Browser}, nil
			case errors.Is(err, usagemeter.ErrNotFound):
				fmt.Fprintln(out, "No claude.ai session found in a local browser — sign in there, or paste the key below.")
			default:
				fmt.Fprintf(out, "Could not read it from the browser: %v\n", err)
			}
		}
	}
	fmt.Fprintln(out, "\nTokenOps needs the session cookie your browser already holds:")
	fmt.Fprintln(out, "  1. open https://claude.ai and sign in")
	fmt.Fprintln(out, "  2. open developer tools (⌥⌘I on macOS, F12 elsewhere)")
	fmt.Fprintln(out, "  3. Application → Storage → Cookies → https://claude.ai")
	fmt.Fprintln(out, "  4. copy the value of the `sessionKey` row (it starts sk-ant-sid...)")
	fmt.Fprintln(out, "  Tip: if macOS blocks browser storage or Cloudflare refuses the request, use --paste-request.")
	fmt.Fprintln(out, "\nIt is sent only to claude.ai, and stored in your local config.")
	key, err := readSecret(cmd, "\nPaste sessionKey: ")
	if err != nil {
		return browserSession{}, err
	}
	return browserSession{key: strings.TrimSpace(key)}, nil
}

const maxCopiedRequestBytes = 1 << 20

// readCopiedRequest keeps interactive credential input hidden, while reading
// the complete command from a pipe. Chrome's "Copy as cURL" output is
// multiline, so the ordinary single-line secret reader truncates it before
// the cookie flags when stdin is piped from a clipboard command.
func readCopiedRequest(cmd *cobra.Command, prompt string) (string, error) {
	fmt.Fprint(cmd.OutOrStdout(), prompt)
	in, ok := cmd.InOrStdin().(*os.File)
	if ok && term.IsTerminal(int(in.Fd())) {
		var lines []string
		for {
			b, err := term.ReadPassword(int(in.Fd()))
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout())
				return "", fmt.Errorf("read copied request: %w", err)
			}
			line := string(b)
			lines = append(lines, line)
			if !curlLineContinues(line) {
				break
			}
		}
		fmt.Fprintln(cmd.OutOrStdout())
		return strings.Join(lines, "\n"), nil
	}

	b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), maxCopiedRequestBytes+1))
	if err != nil {
		return "", fmt.Errorf("read copied request: %w", err)
	}
	if len(b) > maxCopiedRequestBytes {
		return "", errors.New("copied request is too large; nothing was written")
	}
	if len(b) == 0 {
		return "", errors.New("read copied request: empty input")
	}
	return string(b), nil
}

func curlLineContinues(line string) bool {
	return strings.HasSuffix(strings.TrimSpace(line), `\`)
}

// botCheckAdvice says what actually renews Cloudflare's clearance cookie: a
// page load in the browser it belongs to. The generic "session cookies
// rotate, sign in again" sent operators to re-authenticate a session that
// was never the problem.
func botCheckAdvice(browser string) error {
	where := "your browser"
	if browser != "" {
		where = browser
	}
	return fmt.Errorf("%w\n  open https://claude.ai in %s — one page load renews it — then run this again. Nothing was written",
		usagemeter.ErrBotCheck, where)
}
