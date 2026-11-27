package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Connect… on a row whose one action is granting another application's
// sign-in (ADR 0013; Cursor's own sign-in) connects it in place rather than
// handing over a command. The panel shows the consent the daemon serves
// (GET /api/connections/connect), the same words the CLI prints before
// y/N, with Allow and Not now. Allow asks the daemon to grant exactly that
// (POST /api/connections/connect), which reads it once, verifies it with
// the vendor, records the grant without the token and audits it; the row
// then says what happened. The app never reads a sign-in itself.

// consentView is the consent a grant row shows before Allow.
type consentView struct {
	// Heading names what connects with what: "Connect Cursor with
	// Cursor's own sign-in".
	Heading string `json:"heading"`
	// What, Host and Stored are the daemon's words, as the CLI prints
	// them.
	What   string `json:"what"`
	Host   string `json:"host"`
	Stored string `json:"stored"`
	// Keychain warns, before Allow, that macOS will ask; empty when it
	// will not.
	Keychain string `json:"keychain,omitempty"`
}

// connectResult is what Allow did, said in the row.
type connectResult struct {
	Connected bool   `json:"connected"`
	Text      string `json:"text"`
}

// daemonConsent is GET /api/connections/connect.
type daemonConsent struct {
	Name     string `json:"name"`
	App      string `json:"app"`
	What     string `json:"what"`
	Host     string `json:"host"`
	Keychain string `json:"keychain"`
	Stored   string `json:"stored"`
}

// connectState is what the panel was shown, and what Allow did, per
// provider; Go holds it, so Allow sends what was shown, never text from
// the panel.
type connectState struct {
	shown  *daemonConsent
	err    string
	result *connectResult
}

// connectTimeout bounds Allow: macOS's Keychain prompt waits for the
// operator for up to two minutes, then the vendor is asked.
const connectTimeout = 3 * time.Minute

// connectAgain is when the panel reads again after a grant: the daemon
// restarts to read it, then polls the vendor.
var connectAgain = []time.Duration{8 * time.Second, 30 * time.Second}

// grantRow is the grant row the panel showed for provider.
func (m *menubar) grantRow(provider string) (connectionRow, error) {
	r, err := m.rowFor(provider)
	if err != nil {
		return connectionRow{}, err
	}
	if !r.Grant {
		return connectionRow{}, errBadConnection
	}
	return r, nil
}

// openConsent reads, from the daemon, what granting the row's sign-in would
// read, for the panel to show. Nothing is read or granted.
func (m *menubar) openConsent(ctx context.Context, in connectionInput) error {
	r, err := m.grantRow(in.Provider)
	if err != nil {
		return err
	}
	var c daemonConsent
	err = m.daemon.do(ctx, http.MethodGet, "/api/connections/connect?provider="+url.QueryEscape(r.Provider), nil, &c)
	st := connectState{}
	var se *statusError
	switch {
	case err == nil:
		st.shown = &c
	case errors.As(err, &se):
		st.err = se.msg
	default:
		st.err = "TokenOps couldn't look at " + rowName(r) + "'s sign-in just now. Try again in a moment."
	}
	m.mu.Lock()
	if m.connects == nil {
		m.connects = map[string]connectState{}
	}
	m.connects[r.Provider] = st
	m.mu.Unlock()
	return nil
}

// connect asks the daemon to grant exactly the consent the panel showed.
// What happened is said in the row; only a row with no consent shown is an
// error.
func (m *menubar) connect(ctx context.Context, in connectionInput) error {
	r, err := m.grantRow(in.Provider)
	if err != nil {
		return err
	}
	m.mu.Lock()
	st, ok := m.connects[r.Provider]
	m.mu.Unlock()
	if !ok || st.shown == nil {
		return errBadConnection
	}
	body := map[string]string{"provider": r.Provider, "what": st.shown.What, "host": st.shown.Host}
	var out struct {
		Summary string `json:"summary"`
	}
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	err = m.daemon.doWithin(ctx, connectTimeout, http.MethodPost, "/api/connections/connect", body, &out)
	var se *statusError
	switch {
	case err == nil:
		if out.Summary == "" {
			out.Summary = "Connected — reading " + rowName(r) + "'s plan"
		}
		st.result = &connectResult{Connected: true, Text: out.Summary}
	case errors.As(err, &se):
		st.result = &connectResult{Text: se.msg}
	default:
		st.result = &connectResult{Text: "The TokenOps daemon didn't answer, so nothing was granted. Try again in a moment."}
	}
	m.mu.Lock()
	m.connects[r.Provider] = st
	m.mu.Unlock()
	if err == nil {
		m.readAgain(ctx)
	}
	return nil
}

// readAgain reads the daemon again as it restarts with the grant and
// reads the plan, so the row turns into the plan without waiting a minute.
func (m *menubar) readAgain(ctx context.Context) {
	if len(connectAgain) == 0 {
		return
	}
	// Not tied to ctx: a command's context ends when it returns.
	bg := context.WithoutCancel(ctx)
	go func() {
		start := time.Now()
		for _, d := range connectAgain {
			time.Sleep(time.Until(start.Add(d)))
			m.refresh(bg)
		}
	}()
}

// withConnects puts on rows what the panel was shown and what Allow did,
// and forgets it for a tool no longer listed. The caller holds m.mu.
func (m *menubar) withConnects(rows []connectionRow) []connectionRow {
	listed := map[string]bool{}
	out := make([]connectionRow, len(rows))
	for i, r := range rows {
		listed[r.Provider] = true
		if st, ok := m.connects[r.Provider]; ok && r.Grant {
			r.Consent, r.ConsentError, r.Result = st.view(r), st.err, st.result
		}
		out[i] = r
	}
	for p := range m.connects {
		if !listed[p] {
			delete(m.connects, p)
		}
	}
	return out
}

// view is the consent as the row shows it.
func (st connectState) view(r connectionRow) *consentView {
	c := st.shown
	if c == nil {
		return nil
	}
	name, app := c.Name, c.App
	if name == "" {
		name = rowName(r)
	}
	if app == "" {
		app = name
	}
	v := &consentView{Heading: "Connect " + name + " with " + app + "'s own sign-in", What: c.What, Host: c.Host, Stored: c.Stored}
	if c.Keychain != "" {
		v.Keychain = "macOS will ask to allow TokenOps to use “" + c.Keychain + "”. Choose Always Allow, so TokenOps can read " + name + "'s plan again as it polls."
	}
	return v
}

// rowName is the tool's name as its row says it: "Cursor".
func rowName(r connectionRow) string {
	if name, _, ok := strings.Cut(r.Label, " — "); ok && name != "" {
		return name
	}
	return r.Provider
}

// current is the view the panel last had, with what it was shown since:
// an answer that needs no read of the daemon.
func (m *menubar) current() view {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.last
	v.Connections = m.withConnects(v.Connections)
	v.Ring, v.Refreshing = m.settings.Ring, m.refreshing
	if time.Now().Before(m.noteUntil) {
		v.Note, v.NoteWarn = m.note, m.noteWarn
	}
	return v
}
