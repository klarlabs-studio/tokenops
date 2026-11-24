package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// Below the plan rows the panel lists, muted, each tool the operator uses
// whose plan TokenOps does not read (the glance's connections in state
// not_connected), with the one thing to do. It is not an alert: no badge,
// no notification, no colour change; it is said in place. Connect… shows
// the command, what it needs and where to find it, with Copy command and
// Docs; Don't show asks the daemon to stop suggesting it
// (POST /api/connections/dismiss), which is audited and written to the
// config like `tokenops vendor-usage dismiss`.

// connectionRow is one such tool, as the panel shows it.
type connectionRow struct {
	Provider string `json:"provider"`
	// Label is the row: "Cursor — in use, plan not read".
	Label string `json:"label"`
	// Seen says what was seen, in words.
	Seen    string `json:"seen,omitempty"`
	Command string `json:"command"`
	Needs   string `json:"needs"`
	Where   string `json:"where,omitempty"`
	Docs    string `json:"docs,omitempty"`
}

// glanceConnections is the part of GET /api/glance the rows are made from.
type glanceConnections struct {
	Connections []struct {
		Provider string `json:"provider"`
		State    string `json:"state"`
		Summary  string `json:"summary"`
		Evidence []struct {
			Detail string `json:"detail"`
		} `json:"evidence"`
		Action *struct {
			Command string `json:"command"`
			Needs   string `json:"needs"`
			Where   string `json:"where"`
			Docs    string `json:"docs"`
		} `json:"action"`
	} `json:"connections"`
}

// connectionRows are the glance's tools in use whose plan is not read and
// that have a command: nothing for a daemon from before connections.
func connectionRows(glance json.RawMessage) []connectionRow {
	var g glanceConnections
	if len(glance) == 0 || json.Unmarshal(glance, &g) != nil {
		return nil
	}
	var out []connectionRow
	for _, c := range g.Connections {
		if c.State != "not_connected" || c.Action == nil || !validProvider(c.Provider) {
			continue
		}
		seen := make([]string, 0, len(c.Evidence))
		for _, e := range c.Evidence {
			seen = append(seen, e.Detail)
		}
		row := connectionRow{Provider: c.Provider, Label: c.Summary, Seen: strings.Join(seen, " · "),
			Command: c.Action.Command, Needs: c.Action.Needs, Where: c.Action.Where}
		if docsURL(c.Action.Docs) {
			row.Docs = c.Action.Docs
		}
		out = append(out, row)
	}
	return out
}

var providerID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func validProvider(p string) bool { return providerID.MatchString(p) }

// docsURL reports whether u is TokenOps' own documentation, the only page
// the panel opens.
func docsURL(u string) bool {
	return strings.HasPrefix(u, "https://github.com/klarlabs-studio/tokenops/")
}

// connectionInput names the provider a connection command is about.
type connectionInput struct {
	Provider string `json:"provider"`
}

// errBadConnection is a provider the panel did not show.
var errBadConnection = errors.New("not a tool the panel lists as not connected")

// rowFor is the row the panel showed for provider in the last view. The
// command copied and the page opened come from it, never from the panel.
func (m *menubar) rowFor(provider string) (connectionRow, error) {
	if !validProvider(provider) {
		return connectionRow{}, errBadConnection
	}
	m.mu.Lock()
	rows := m.last.Connections
	m.mu.Unlock()
	for _, r := range rows {
		if r.Provider == provider {
			return r, nil
		}
	}
	return connectionRow{}, errBadConnection
}

// copyText puts text on the clipboard; a variable so tests do not.
var copyText = func(text string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("copy it from the panel")
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// copyConnect copies the row's command and says what to do with it.
func (m *menubar) copyConnect(in connectionInput) error {
	r, err := m.rowFor(in.Provider)
	if err != nil {
		return err
	}
	if err := copyText(r.Command); err != nil {
		m.setNote("Couldn't copy it. Run in a terminal: "+r.Command, true)
		return nil
	}
	note := "Copied. Run it in a terminal; it needs " + r.Needs + "."
	if strings.Contains(r.Command, "…") {
		note = "Copied. In a terminal, put in place of … " + r.Needs + ", then run it."
	}
	m.setNote(note, false)
	return nil
}

// openConnectDocs opens the row's docs page.
func (m *menubar) openConnectDocs(in connectionInput) error {
	r, err := m.rowFor(in.Provider)
	if err != nil || r.Docs == "" {
		return errBadConnection
	}
	return openFile(r.Docs)
}

// dismissConnection asks the daemon to stop suggesting provider.
func (m *menubar) dismissConnection(ctx context.Context, in connectionInput) error {
	r, err := m.rowFor(in.Provider)
	if err != nil {
		return err
	}
	var out json.RawMessage
	if err := m.daemon.do(ctx, http.MethodPost, "/api/connections/dismiss", in, &out); err != nil {
		return err
	}
	name, _, _ := strings.Cut(r.Label, " — ")
	m.setNote(name+" won't be suggested again. `tokenops vendor-usage dismiss "+r.Provider+" --undo` takes that back.", false)
	return nil
}
