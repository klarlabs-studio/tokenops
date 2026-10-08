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

// verifyGateway reads a gateway's budget once at an address. Tests replace
// it so no gateway is called.
var verifyGateway = providersetup.VerifyGateway

// chainCredential finds a provider's credential in its vendor's chain on
// this machine. Tests replace it so no real credential is read.
var chainCredential = providersetup.FromChain

// providerSetupOptions are the choices `setup <provider>` was given.
type providerSetupOptions struct {
	configPath string
	restart    bool
	browser    string
	paste      bool
	// scope is --scope; scopeSet is whether it was given at all, so a
	// setup run without it keeps the scope already stored.
	scope    string
	scopeSet bool
}

// runProviderSetup connects any registry provider read with an API key or
// a browser session: it finds the credential, proves it with one reading,
// and only then stores it. Nothing here is specific to a vendor; the
// provider's descriptor says what to ask for.
func runProviderSetup(cmd *cobra.Command, p providersetup.Provider, opts providerSetupOptions) error {
	out := cmd.OutOrStdout()
	if err := providersetup.CheckScope(p, opts.scope); err != nil {
		return fmt.Errorf("%w. Nothing was written", err)
	}
	path, err := resolveMutableConfigPath(opts.configPath)
	if err != nil {
		return err
	}
	scope := strings.TrimSpace(opts.scope)
	if !opts.scopeSet && p.Scope != "" {
		if cfg, err := readMutableConfig(path); err == nil {
			scope = cfg.VendorUsage.Accounts.Scopes[p.ID]
		}
	}
	fmt.Fprintf(out, "Connecting %s.\n", p.Name)
	if scope != "" {
		fmt.Fprintf(out, "Reading the scope %q.\n", scope)
	}
	if p.Gateway {
		return runGatewaySetup(cmd, p, opts)
	}
	if p.Chain {
		return runChainSetup(cmd, p, opts)
	}

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
	lines, err := verifyProvider(ctx, p.ID, key, scope)
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

	cfg, err := readMutableConfig(path)
	if err != nil {
		return err
	}
	providersetup.Apply(&cfg, p.ID, key, browser != "", browser)
	if p.Scope != "" {
		providersetup.ApplyScope(&cfg, p.ID, scope)
	}
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

// loginProvider signs in with a username and password. Tests replace it
// so no vendor is called.
var loginProvider = providersetup.Login

// providerCredential gets the key or session: a session from the browser
// unless --paste, a session from a password sign-in for a vendor that has
// one, otherwise typed at a prompt that does not echo it.
func providerCredential(cmd *cobra.Command, p providersetup.Provider, opts providerSetupOptions) (key, browser string, err error) {
	out := cmd.OutOrStdout()
	if p.Browser && !p.Cookie.PasteOnly && !opts.paste {
		fmt.Fprintf(out, "\nLooking for your %s session in a local browser.\n", p.CookieHosts())
		fmt.Fprintf(out, "macOS may ask to let tokenops read your browser's \"Safe Storage\" item from the Keychain:\n"+
			"TokenOps uses it to read %s (%s) and nothing else. --paste skips the browser.\n",
			p.CookieHosts(), p.CookieNames())
		key, browser, err := providersetup.FromBrowser(cmd.Context(), p, opts.browser,
			browsercookie.InteractiveKeychainWait, keychainDisabled(opts.configPath))
		switch {
		case err == nil:
			return key, browser, nil
		case errors.Is(err, providersetup.ErrNotFound):
			fmt.Fprintf(out, "No %s session found in a local browser: sign in there, or paste it below.\n", p.CookieHosts())
		default:
			fmt.Fprintf(out, "Could not read it from the browser: %v\n", err)
		}
	}
	if p.KeychainServer != "" && !opts.paste {
		fmt.Fprintf(out, "\nReading %s's own sign-in from the Keychain (the internet password for %s).\n", p.Name, p.KeychainServer)
		fmt.Fprintf(out, "macOS may ask to let tokenops read it. TokenOps stores the token in your config, sends it only to %s,\n"+
			"never reads the Keychain for it again in the background and never refreshes it: when %s stops accepting it,\n"+
			"run this again. --paste types it instead.\n", p.Name, p.Name)
		key, err := providersetup.FromKeychain(cmd.Context(), p, browsercookie.InteractiveKeychainWait, keychainDisabled(opts.configPath))
		if err == nil {
			return key, "", nil
		}
		fmt.Fprintf(out, "Could not read it from the Keychain: %v\n", err)
	}
	if p.Login && !opts.paste {
		return loginCredential(cmd, p)
	}
	prompt := "\nPaste the API key: "
	if p.KeyFormat != "" {
		prompt = fmt.Sprintf("\nPaste it as %s: ", p.KeyFormat)
	}
	switch {
	case p.Login:
		prompt = fmt.Sprintf("\nPaste the %s session token: ", p.Name)
	case p.Browser && p.Key:
		prompt = fmt.Sprintf("\nPaste the API key, or the Cookie header for %s: ", p.CookieHosts())
	case p.Browser && p.Cookie.PasteOnly && len(p.Cookie.Names) == 0:
		fmt.Fprintf(out, "\nIn your browser's developer tools, copy the Cookie request header of a request to %s.\n", p.CookieHosts())
		prompt = fmt.Sprintf("\nPaste the Cookie header for %s: ", p.CookieHosts())
	case p.Browser:
		prompt = fmt.Sprintf("\nPaste the Cookie header for %s (%s): ", p.CookieHosts(), p.CookieNames())
	}
	if !p.Browser && len(p.EnvVars) > 0 {
		fmt.Fprintf(out, "\n(%s is read without setup, when it is set.)\n", strings.Join(p.EnvVars, " or "))
	}
	fmt.Fprintf(out, "It is sent only to %s, and stored in your local config.\n", p.Name)
	key, err = readSecret(cmd, prompt)
	key = strings.TrimSpace(key)
	if p.Browser && len(key) > 7 && strings.EqualFold(key[:7], "cookie:") {
		// The header as devtools copies it, name and all.
		key = strings.TrimSpace(key[7:])
	}
	return key, "", err
}

// runGatewaySetup connects a gateway the operator runs or subscribes to:
// its address, then the key, proved with one reading at that address
// before either is stored.
func runGatewaySetup(cmd *cobra.Command, p providersetup.Provider, opts providerSetupOptions) error {
	out := cmd.OutOrStdout()
	prompt := "\nThe gateway's base URL: "
	if p.DefaultBaseURL != "" {
		prompt = fmt.Sprintf("\nThe gateway's base URL (empty for %s): ", p.DefaultBaseURL)
	}
	base, err := readLine(cmd, prompt)
	if err != nil {
		return err
	}
	if base = strings.TrimSpace(base); base == "" {
		base = p.DefaultBaseURL
	}
	if base == "" {
		return errors.New("no address entered; nothing was written")
	}
	if len(p.EnvVars) > 0 && p.BaseURLEnv != "" {
		fmt.Fprintf(out, "(%s with %s is read without setup, when they are set.)\n", strings.Join(p.EnvVars, " or "), p.BaseURLEnv)
	}
	fmt.Fprintf(out, "The key is sent only to %s, and stored in your local config.\n", base)
	key, err := readSecret(cmd, "\nPaste the API key: ")
	if err != nil {
		return err
	}
	if key = strings.TrimSpace(key); key == "" {
		return errors.New("nothing entered; nothing was written")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	progress := startActivity(cmd.ErrOrStderr(), "Checking it with "+p.Name)
	lines, err := verifyGateway(ctx, p.ID, base, key)
	if err != nil {
		progress.failure(p.Name + " did not accept it")
		if errors.Is(err, providersetup.ErrRefused) {
			return fmt.Errorf("%s refused that key. Nothing was written", p.Name)
		}
		return fmt.Errorf("could not read %s at %s: %w. Nothing was written", p.Name, base, err)
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
	providersetup.ApplyGateway(&cfg, p.ID, base, key)
	if err := writeMutableConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nwrote %s (the key is stored there and sent only to %s)\n", path, base)
	applyRestart(out, opts.restart, false)
	return nil
}

// readLine reads one line, echoed: an address, not a secret. It reads a
// byte at a time so the key that follows on a piped stdin is left for
// readSecret.
func readLine(cmd *cobra.Command, prompt string) (string, error) {
	fmt.Fprint(cmd.OutOrStdout(), prompt)
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := cmd.InOrStdin().Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				return b.String(), nil
			}
			b.WriteByte(buf[0])
		}
		if err != nil {
			if b.Len() > 0 {
				return b.String(), nil
			}
			return "", fmt.Errorf("read input: %w", err)
		}
	}
}

// runChainSetup opts in a provider read with its vendor's own credential
// chain (AWS's environment and shared credentials file): it finds the
// credential, proves it with one reading, and stores only that the daemon
// may read it, never the credential.
func runChainSetup(cmd *cobra.Command, p providersetup.Provider, opts providerSetupOptions) error {
	out := cmd.OutOrStdout()
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	key, origin, err := chainCredential(ctx, p.ID)
	if err != nil {
		return fmt.Errorf("no credential for %s found on this machine: %w. Nothing was written", p.Name, err)
	}
	fmt.Fprintf(out, "Found a credential in %s. It is sent only to %s and not stored.\n", origin, p.Name)
	progress := startActivity(cmd.ErrOrStderr(), "Checking it with "+p.Name)
	lines, err := verifyProvider(ctx, p.ID, key, "")
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
	providersetup.ApplyChain(&cfg, p.ID)
	if err := writeMutableConfig(path, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nwrote %s: the daemon reads the credential the same way as it polls, and stores none\n", path)
	applyRestart(out, opts.restart, false)
	return nil
}

// loginCredential signs in with a username (echoed) and a password (never
// echoed), and returns the session token the vendor issued. Only the token
// is stored; the password is not kept, written or logged.
func loginCredential(cmd *cobra.Command, p providersetup.Provider) (string, string, error) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\nSign in to %s. The password is sent only to %s to sign in, and is not stored:\n"+
		"TokenOps keeps the session token %s issues. --paste types a token instead.\n", p.Name, p.Name, p.Name)
	user, err := readLine(cmd, "Username (email or phone): ")
	if err != nil {
		return "", "", err
	}
	password, err := readSecret(cmd, "Password: ")
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	token, err := loginProvider(ctx, p.ID, strings.TrimSpace(user), strings.TrimRight(password, "\r\n"))
	if err != nil {
		if errors.Is(err, providersetup.ErrRefused) {
			return "", "", fmt.Errorf("%s did not accept that username and password. Nothing was written", p.Name)
		}
		return "", "", fmt.Errorf("could not sign in to %s: %w. Nothing was written", p.Name, err)
	}
	return token, "", nil
}
