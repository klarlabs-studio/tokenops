package providers

import "strings"

// DocsConnect is the page that says, per credential, how to connect a plan
// TokenOps found in use but cannot read yet.
const DocsConnect = "https://github.com/klarlabs-studio/tokenops/blob/main/docs/reference/cli.md#connect-a-tool-tokenops-found"

// Presence is a cheap, read-only sign that a provider's tool is on this
// machine: an application bundle, a directory the tool keeps, or its CLI
// on PATH. A probe only asks whether the path exists: it opens no file,
// reads no credential, and touches nothing macOS guards behind a prompt
// (no ~/Library, Desktop, Documents or Downloads). Exactly one field is
// set.
type Presence struct {
	// App is a macOS application bundle ("Cursor.app"), looked for in
	// /Applications and ~/Applications.
	App string
	// Dir is a directory: "~/" is the home directory and
	// "$XDG_CONFIG_HOME/" is XDG_CONFIG_HOME, else ~/.config.
	Dir string
	// Binary is an executable looked for on PATH.
	Binary string
}

// Connect is what the operator does to connect a source TokenOps cannot
// connect by itself: a credential to hand over or a consent to give. A
// source with no secret and no question is connected without asking.
type Connect struct {
	// Command is the one command that connects it, as typed.
	Command string
	// Needs says, in plain words, what credential the command needs.
	Needs string
	// Where says, in one short line, where to find that credential.
	Where string
}

// Action is the one thing to do to connect a provider's plan: the source
// it connects and how.
type Action struct {
	Source string
	Connect
}

// PlanSource reports whether s reads a plan, a balance or spend, rather
// than the per-turn records a harness keeps; or reads the plan's windows
// from those records (WindowsInRecords).
func (s Source) PlanSource() bool { return s.Kind != LocalLog || s.WindowsInRecords }

// Automatic reports whether s reads with nothing from the operator: the
// vendor's own CLI signing its own request, or a file the tool keeps. A
// source the operator still has to install something for says so in its
// Connect.
func (s Source) Automatic() bool {
	return s.Connect == nil && (s.Credential == CLI || s.Credential == LocalFile)
}

// ConnectAction is the one thing the operator does to connect d's plan:
// the first of its plan sources that needs the operator, among its
// subscription sources when it has any, so a plan is not answered with an
// organisation's spend. False when nothing the operator does connects it:
// a provider in the catalog only, or one whose plan is read by itself.
func (d Descriptor) ConnectAction() (Action, bool) {
	subscription := false
	for _, s := range d.Sources {
		subscription = subscription || s.Kind == Subscription
	}
	for _, s := range d.Sources {
		if s.Kind == LocalLog || (subscription && s.Kind != Subscription) || s.Automatic() {
			continue
		}
		if c, ok := d.connectOf(s); ok {
			return Action{Source: s.Tag, Connect: c}, true
		}
	}
	return Action{}, false
}

// connectOf is how s is connected: its own Connect, or for an account or
// gateway reader, `tokenops vendor-usage setup <id>` with what its
// credential is in plain words.
func (d Descriptor) connectOf(s Source) (Connect, bool) {
	if s.Connect != nil {
		return *s.Connect, true
	}
	if s.Reader != AccountReader && s.Reader != GatewayReader {
		return Connect{}, false
	}
	id := string(d.ID)
	c := Connect{Command: "tokenops vendor-usage setup " + id}
	switch s.Credential {
	case APIKey:
		c.Needs = "an API key from " + d.DisplayName
		if s.KeyFormat != "" {
			c.Needs = s.KeyFormat
		}
		if s.Reader == GatewayReader {
			c.Needs = "the gateway's address and a key for it"
		}
	case AdminKey:
		c.Needs = "an organisation admin key"
		if s.KeyFormat != "" {
			c.Needs = s.KeyFormat
		}
	case BrowserCookie:
		c.Needs = "your browser session on " + d.DisplayName + "'s website"
		if s.Cookie != nil {
			c.Needs = "your browser session on " + s.Cookie.Host
		}
		c.Where = "sign in there in your browser first; setup reads the session from it (macOS may ask once)"
		if s.Cookie != nil && s.Cookie.PasteOnly {
			c.Where = "setup asks for the Cookie header from your browser's developer tools"
		}
	case PasswordLogin:
		c.Needs = "your " + d.DisplayName + " username and password, once; only the session token is kept"
	case CredentialChain:
		c.Needs = "the cloud credentials already on this machine; nothing is stored"
	case AppKeychain:
		c.Needs = "your yes to read " + d.DisplayName + "'s own sign-in from the Keychain (macOS asks first)"
	case AppLogin:
		c.Command += " --use-app-login"
		c.Needs = "your yes to read " + d.appLoginName(s) + "'s own sign-in; setup shows exactly what it reads first"
	default:
		return Connect{}, false
	}
	if c.Where == "" && len(s.EnvVars) > 0 {
		c.Where = "or set " + strings.Join(s.EnvVars, " or ") + " for the daemon"
	}
	if c.Where == "" && s.Credential == APIKey && s.Reader == AccountReader && len(d.EnvVars) > 0 {
		c.Where = "or set " + strings.Join(d.EnvVars, " or ") + " where your harness runs"
	}
	return c, true
}

// appLoginName names the application whose sign-in an AppLogin source
// reads.
func (d Descriptor) appLoginName(s Source) string {
	if len(s.AppLogins) > 0 && s.AppLogins[0].App != "" {
		return s.AppLogins[0].App
	}
	return d.DisplayName
}

// ConnectFor is how d's source tag is connected, or connected again once
// it is refused.
func (d Descriptor) ConnectFor(tag string) (Action, bool) {
	for _, s := range d.Sources {
		if s.Tag != tag {
			continue
		}
		if c, ok := d.connectOf(s); ok {
			return Action{Source: s.Tag, Connect: c}, true
		}
	}
	return Action{}, false
}
