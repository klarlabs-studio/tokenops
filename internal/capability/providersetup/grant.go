package providersetup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	"go.klarlabs.de/tokenops/internal/infra/applogin"
	"go.klarlabs.de/tokenops/internal/infra/keychain"
)

// The grant flow's core (ADR 0013), shared by `tokenops vendor-usage setup
// <id> --use-app-login` and the daemon's POST /api/connections/connect, which
// the menu bar's Allow calls: after the operator's yes to exactly what is
// read, read it once against the vendor and, only when the vendor accepts
// it, record the grant. Both surfaces show the same Consent first.

// StoredNothingSecret is what a grant keeps, as the operator is told.
const StoredNothingSecret = "nothing secret; only this grant, in your config"

// Consent is what the operator is shown before granting: the same words
// the CLI prints before y/N. Nothing secret has been read to make it.
type Consent struct {
	Provider string `json:"provider"`
	// Name is the provider's name ("Cursor").
	Name string `json:"name"`
	// App is the application whose sign-in is read.
	App string `json:"app"`
	// What says exactly which item and fields are read.
	What string `json:"what"`
	// Host is the one host the token is sent to.
	Host string `json:"host"`
	// Keychain is the Keychain item macOS asks the operator to allow when
	// the sign-in is sealed under one ("Cursor Safe Storage"); empty when
	// macOS asks nothing.
	Keychain string `json:"keychain,omitempty"`
	// Stored says what is kept: nothing secret.
	Stored string `json:"stored"`
}

// Consent is what the operator is shown before granting a for provider id.
func (a AppLogin) Consent(id string) Consent {
	c := Consent{Provider: id, Name: id, App: a.App, What: a.What, Host: a.Host, Stored: StoredNothingSecret}
	if d, ok := providers.Lookup(id); ok {
		c.Name = d.DisplayName
	}
	if u := a.located.Spec.Unlock; u != nil {
		c.Keychain = u.Service
	}
	return c
}

// VerifyFunc reads a sign-in once and asks the vendor with it, returning
// what the vendor reported. VerifyAppLogin is the real one.
type VerifyFunc func(ctx context.Context, id string, a AppLogin, env applogin.Env) ([]string, error)

// GrantDeps is what a grant reads with.
type GrantDeps struct {
	// Env is where the sign-in is; the zero value is this machine with
	// the Keychain read only quietly.
	Env applogin.Env
	// Verify asks the vendor; nil is VerifyAppLogin.
	Verify VerifyFunc
	// Now stamps the grant; nil is time.Now.
	Now func() time.Time
}

func (d GrantDeps) verify() VerifyFunc {
	if d.Verify != nil {
		return d.Verify
	}
	return VerifyAppLogin
}

func (d GrantDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// NotAcceptedError is a sign-in that could not be read, or that the vendor
// did not accept: nothing was written.
type NotAcceptedError struct{ Err error }

func (e *NotAcceptedError) Error() string { return e.Err.Error() }
func (e *NotAcceptedError) Unwrap() error { return e.Err }

// GrantAppLogin reads a once against the vendor and, only when the vendor
// accepts it, records the grant in the config at path. It returns what the
// vendor reported. A sign-in that cannot be read or is refused is a
// *NotAcceptedError and leaves the config as it was. The token is held in
// memory for the one reading only.
func GrantAppLogin(ctx context.Context, path, id string, a AppLogin, deps GrantDeps) ([]string, error) {
	lines, err := deps.verify()(ctx, id, a, deps.Env)
	if err != nil {
		return nil, &NotAcceptedError{Err: err}
	}
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return nil, err
	}
	ApplyGrant(&cfg, id, a, deps.now())
	if err := config.WriteMutable(path, cfg); err != nil {
		return nil, err
	}
	return lines, nil
}

// Shown is the consent a surface showed the operator: a grant is made only
// for exactly this item and host.
type Shown struct {
	What string `json:"what"`
	Host string `json:"host"`
}

// Connected is a grant made from a surface.
type Connected struct {
	Provider string `json:"provider"`
	App      string `json:"app"`
	What     string `json:"what"`
	Host     string `json:"host"`
	// Summary is the line a surface shows: "Connected — reading Cursor's
	// plan".
	Summary string `json:"summary"`
	// Reports is what the vendor reported with the sign-in.
	Reports []string `json:"reports"`
	// Config is the file the grant was written to.
	Config string `json:"config"`
}

// ConnectFailure says why a connect granted nothing.
type ConnectFailure string

// The failures.
const (
	// ConnectNotShown: the request did not carry the consent shown.
	ConnectNotShown ConnectFailure = "not_shown"
	// ConnectNotAppLogin: no other application's sign-in reads it.
	ConnectNotAppLogin ConnectFailure = "not_app_login"
	// ConnectNoSignIn: the sign-in is not on this machine.
	ConnectNoSignIn ConnectFailure = "no_sign_in"
	// ConnectChanged: what would be read is not what was shown.
	ConnectChanged ConnectFailure = "changed"
	// ConnectRefused: the vendor refused the sign-in (expired, signed out).
	ConnectRefused ConnectFailure = "refused"
	// ConnectKeychain: macOS did not allow the Keychain item, or the
	// Keychain is switched off.
	ConnectKeychain ConnectFailure = "keychain"
	// ConnectUnreadable: the sign-in or the vendor could not be read.
	ConnectUnreadable ConnectFailure = "unreadable"
)

// ConnectError is a connect that granted nothing, said in plain words for
// the operator. Nothing was written. It never carries the token.
type ConnectError struct {
	Failure ConnectFailure
	msg     string
	err     error
}

func (e *ConnectError) Error() string { return e.msg }
func (e *ConnectError) Unwrap() error { return e.err }

// FindConsent is what granting provider id would read, for a surface to
// show before asking: nothing secret is read.
func FindConsent(ctx context.Context, id string, env applogin.Env) (Consent, error) {
	_, a, err := findForConnect(ctx, id, env)
	if err != nil {
		return Consent{}, err
	}
	return a.Consent(id), nil
}

// ConnectAppLogin grants provider id's sign-in from a surface, on the
// operator's yes to shown: the item is found again, and granted only when
// it is exactly what was shown (GrantAppLogin). A failure is a
// *ConnectError in plain words; nothing is written.
func ConnectAppLogin(ctx context.Context, path, id string, shown Shown, deps GrantDeps) (Connected, error) {
	if shown.What == "" || shown.Host == "" {
		return Connected{}, &ConnectError{Failure: ConnectNotShown,
			msg: "show the operator what is read first (GET /api/connections/connect) and send its what and host"}
	}
	p, a, err := findForConnect(ctx, id, deps.Env)
	if err != nil {
		return Connected{}, err
	}
	if a.What != shown.What || a.Host != shown.Host {
		return Connected{}, &ConnectError{Failure: ConnectChanged,
			msg: "What TokenOps would read has changed since you were shown it; look again. Nothing was read."}
	}
	lines, err := GrantAppLogin(ctx, path, p.ID, a, deps)
	if err != nil {
		return Connected{}, notAccepted(p, a, err)
	}
	return Connected{Provider: p.ID, App: a.App, What: a.What, Host: a.Host, Reports: lines, Config: path,
		Summary: "Connected — reading " + p.Name + "'s plan"}, nil
}

// findForConnect finds provider id's sign-in without reading it.
func findForConnect(ctx context.Context, id string, env applogin.Env) (Provider, AppLogin, error) {
	p, ok := Lookup(id)
	if !ok || !p.AppLogin {
		name := id
		if ok {
			name = p.Name
		}
		return Provider{}, AppLogin{}, &ConnectError{Failure: ConnectNotAppLogin,
			msg: name + " is not read with another application's sign-in"}
	}
	a, err := FindAppLogin(ctx, p.ID, env)
	if err != nil {
		return Provider{}, AppLogin{}, notAccepted(p, AppLogin{App: p.Name}, err)
	}
	return p, a, nil
}

// notAccepted says in plain words why a sign-in granted nothing.
func notAccepted(p Provider, a AppLogin, err error) error {
	app := a.App
	if app == "" {
		app = p.Name
	}
	var nae *NotAcceptedError
	switch {
	case errors.Is(err, applogin.ErrNotFound):
		return &ConnectError{Failure: ConnectNoSignIn, err: err,
			msg: "No " + app + " sign-in was found on this Mac. Sign in to " + app + ", then try again."}
	case errors.Is(err, ErrRefused):
		return &ConnectError{Failure: ConnectRefused, err: err,
			msg: p.Name + " did not accept " + app + "'s sign-in; it may have expired. Sign in to " + app + ", then try again."}
	case errors.Is(err, keychain.ErrDisabled):
		return &ConnectError{Failure: ConnectKeychain, err: err,
			msg: "Keychain access is switched off (keychain.disabled), so " + app + "'s sign-in cannot be opened. Nothing was read."}
	case errors.Is(err, keychain.ErrDenied), errors.Is(err, keychain.ErrInteractionRequired):
		item := a.Consent(p.ID).Keychain
		if item == "" {
			item = app + "'s Keychain item"
		} else {
			item = fmt.Sprintf("%q", item)
		}
		return &ConnectError{Failure: ConnectKeychain, err: err,
			msg: "macOS did not allow TokenOps to use " + item + ", so nothing was read. Try again and choose Allow."}
	case errors.As(err, &nae):
		return &ConnectError{Failure: ConnectUnreadable, err: err,
			msg: "Couldn't read " + p.Name + "'s plan with " + app + "'s sign-in: " + nae.Err.Error() + ". Nothing was written."}
	}
	return err
}
