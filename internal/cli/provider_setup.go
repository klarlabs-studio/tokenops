package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/capability/providersetup"
	"go.klarlabs.de/tokenops/internal/infra/browsercookie"
)

// verifyProvider reads a provider's account once with a credential. Tests
// replace it so no vendor is called.
var verifyProvider = providersetup.Verify

// providerSetupOptions are the choices `setup <provider>` was given.
type providerSetupOptions struct {
	configPath string
	restart    bool
	browser    string
	paste      bool
}

// runProviderSetup connects any registry provider read with an API key or
// a browser session: it finds the credential, proves it with one reading,
// and only then stores it. Nothing here is specific to a vendor; the
// provider's descriptor says what to ask for.
func runProviderSetup(cmd *cobra.Command, p providersetup.Provider, opts providerSetupOptions) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Connecting %s.\n", p.Name)

	key, browser, err := providerCredential(cmd, p, opts)
	if err != nil {
		return err
	}
	if strings.TrimSpace(key) == "" {
		return errors.New("nothing entered; nothing was written")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	progress := startActivity(cmd.ErrOrStderr(), "Checking it with "+p.Name)
	lines, err := verifyProvider(ctx, p.ID, key)
	if err != nil {
		progress.failure(p.Name + " did not accept it")
		if errors.Is(err, providersetup.ErrRefused) {
			return fmt.Errorf("%s refused that credential. Nothing was written", p.Name)
		}
		return fmt.Errorf("could not read %s: %w. Nothing was written", p.Name, err)
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
	providersetup.Apply(&cfg, p.ID, key, browser != "", browser)
	if err := writeMutableConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nwrote %s (the credential is stored there and sent only to %s)\n", path, p.Name)
	if browser != "" {
		fmt.Fprintf(out, "The daemon re-reads the session from %s when %s refuses the stored one, without ever showing a prompt.\n", browser, p.Name)
	}
	applyRestart(out, opts.restart, false)
	return nil
}

// providerCredential gets the key or session: a session from the browser
// unless --paste, otherwise typed at a prompt that does not echo it.
func providerCredential(cmd *cobra.Command, p providersetup.Provider, opts providerSetupOptions) (key, browser string, err error) {
	out := cmd.OutOrStdout()
	if p.Browser && !opts.paste {
		fmt.Fprintf(out, "\nLooking for your %s session in a local browser.\n", p.CookieHost)
		fmt.Fprintf(out, "macOS may ask to let tokenops read your browser's \"Safe Storage\" item from the Keychain:\n"+
			"TokenOps uses it to read %s's %s cookies and nothing else. --paste skips the browser.\n",
			p.CookieHost, strings.Join(p.CookieNames, ", "))
		key, browser, err := providersetup.FromBrowser(cmd.Context(), p, opts.browser,
			browsercookie.InteractiveKeychainWait, keychainDisabled(opts.configPath))
		switch {
		case err == nil:
			return key, browser, nil
		case errors.Is(err, providersetup.ErrNotFound):
			fmt.Fprintf(out, "No %s session found in a local browser: sign in there, or paste it below.\n", p.CookieHost)
		default:
			fmt.Fprintf(out, "Could not read it from the browser: %v\n", err)
		}
	}
	prompt := "\nPaste the API key: "
	if p.KeyFormat != "" {
		prompt = fmt.Sprintf("\nPaste it as %s: ", p.KeyFormat)
	}
	switch {
	case p.Browser:
		prompt = fmt.Sprintf("\nPaste the Cookie header for %s (%s): ", p.CookieHost, strings.Join(p.CookieNames, ", "))
	case p.PasteCookie:
		fmt.Fprintf(out, "\n%s is read with your browser session, whose cookies TokenOps does not read itself:\n"+
			"in the browser's developer tools, copy the Cookie request header of a request to %s.\n", p.Name, p.CookieHost)
		prompt = fmt.Sprintf("\nPaste the Cookie header for %s: ", p.CookieHost)
	case len(p.EnvVars) > 0:
		fmt.Fprintf(out, "\n(%s is read without setup, when it is set.)\n", strings.Join(p.EnvVars, " or "))
	}
	fmt.Fprintf(out, "It is sent only to %s, and stored in your local config.\n", p.Name)
	key, err = readSecret(cmd, prompt)
	key = strings.TrimSpace(key)
	if (p.Browser || p.PasteCookie) && len(key) > 7 && strings.EqualFold(key[:7], "cookie:") {
		// The header as devtools copies it, name and all.
		key = strings.TrimSpace(key[7:])
	}
	return key, "", err
}
