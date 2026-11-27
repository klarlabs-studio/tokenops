// Package connection says, per provider, whether TokenOps reads the plan
// of a tool the operator uses, and when it does not, the one thing to do
// to connect it.
//
// A connection is a finding about TokenOps' own reach, kept apart from the
// evidence it rests on (turns in the event log, a tool installed on this
// machine), from the readings the sources make, and from any decision: it
// never connects anything itself. Where a source needs no secret and no
// question it is turned on without one; what is left needs the operator, a
// credential or a consent, and is said in place, never pushed.
//
// The package is pure: the capability layer gathers the inputs.
package connection

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
)

// State is what is true of a provider's plan, as a closed set.
type State string

const (
	// Reading: a plan source answered within the window.
	Reading State = "reading"
	// Connected: a plan source is switched on and has not read yet.
	Connected State = "connected"
	// Failing: a plan source is switched on and refused, or has gone
	// quiet.
	Failing State = "failing"
	// NotConnected: the operator uses the tool, or has it installed, and
	// no plan source is on.
	NotConnected State = "not_connected"
	// Dismissed: as NotConnected, and the operator said not to read it.
	Dismissed State = "dismissed"
)

// Window is how far back evidence of use and a source's last reading
// count: a week, the longest window a plan resets on.
const Window = 7 * 24 * time.Hour

// EvidenceKind is what was seen.
type EvidenceKind string

const (
	// Turns are requests the event log holds for the provider.
	Turns EvidenceKind = "turns"
	// Installed is the tool's app, directory or CLI on this machine.
	Installed EvidenceKind = "installed"
)

// Use is a provider's turns in the event log over Window.
type Use struct {
	Turns  int       `json:"turns"`
	LastAt time.Time `json:"last_at"`
}

// Source is one plan source's state, from the configuration and the
// readers' health.
type Source struct {
	Tag string
	// Switched is whether the operator connected it (enabled it, stored
	// its credential, granted its sign-in), or it is on by default with
	// nothing to give. A source that runs always but has read nothing is
	// not switched.
	Switched bool
	// Health is the reader's freshness state: healthy, idle, stale or
	// failing; empty when unknown.
	Health      string
	LastEventAt time.Time
	LastError   string
}

// Inputs is what classification reads.
type Inputs struct {
	Now time.Time
	// Use is each provider's turns over Window.
	Use map[string]Use
	// Installed is, per provider, what was found of its tool: an app
	// bundle's path, a directory, "<binary> on PATH".
	Installed map[string][]string
	// SignIns is, per provider, whether another application's sign-in its
	// descriptor reads is on this machine, found without reading it. An
	// app-login source is offered only when it is; otherwise the next way
	// in is.
	SignIns map[string]bool
	// Sources are the plan sources' states, by tag; a source absent is
	// off with nothing known.
	Sources []Source
	// Reported are the providers a plan report reads now.
	Reported map[string]bool
	// Dismissed are the providers the operator said not to suggest.
	Dismissed map[string]bool
}

// Evidence is one thing seen that says the operator uses the tool.
type Evidence struct {
	Kind EvidenceKind `json:"kind"`
	// Detail says it in words: "41 turns in the last 7 days",
	// "/Applications/Cursor.app".
	Detail string `json:"detail"`
	Turns  int    `json:"turns,omitempty"`
	// At is the last turn's time; zero for an installed tool.
	At time.Time `json:"at,omitzero"`
}

// Action is the one thing to do to connect the plan.
type Action struct {
	// Source is the source it connects.
	Source string `json:"source"`
	// Command is the command, as typed.
	Command string `json:"command"`
	// Needs is the credential it needs, in plain words.
	Needs string `json:"needs"`
	// Where says where to find it, in one short line.
	Where string `json:"where,omitempty"`
	// Docs is the page that says more.
	Docs string `json:"docs"`
	// Dismiss is the command that stops TokenOps suggesting it.
	Dismiss string `json:"dismiss"`
	// Grant is an action taken by granting another application's sign-in
	// (ADR 0013): a surface can show what is read and connect it in place
	// on the operator's yes (POST /api/connections/connect).
	Grant bool `json:"grant,omitempty"`
}

// Connection is what TokenOps says about one provider's plan.
type Connection struct {
	Provider string `json:"provider"`
	Display  string `json:"display"`
	State    State  `json:"state"`
	// Summary is the line a surface shows: "Cursor — in use, plan not
	// read".
	Summary  string     `json:"summary"`
	Evidence []Evidence `json:"evidence,omitempty"`
	// Source is the plan source the state is about: the one reading, the
	// one failing, or the one the action connects.
	Source string `json:"source,omitempty"`
	// Problem says what is wrong with a failing source.
	Problem string `json:"problem,omitempty"`
	// Action is the one thing to do; absent while reading or waiting.
	Action *Action `json:"action,omitempty"`
}

// Classify says, for every provider in the registry with a plan the
// operator can connect, what is true of it. A provider with no evidence of
// use and no source on is left out.
func Classify(in Inputs) []Connection {
	sources := map[string]Source{}
	for _, s := range in.Sources {
		sources[s.Tag] = s
	}
	var out []Connection
	for _, d := range providers.All() {
		if c, ok := classifyOne(d, in, sources); ok {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank[a.State] != rank[b.State] {
			return rank[a.State] < rank[b.State]
		}
		if ta, tb := turnsOf(a), turnsOf(b); ta != tb {
			return ta > tb
		}
		return a.Provider < b.Provider
	})
	return out
}

// rank orders the states: what fails first, then what the operator can
// connect, then what is fine.
var rank = map[State]int{Failing: 0, NotConnected: 1, Connected: 2, Reading: 3, Dismissed: 4}

func turnsOf(c Connection) int {
	for _, e := range c.Evidence {
		if e.Kind == Turns {
			return e.Turns
		}
	}
	return 0
}

// planState is what d's plan sources say together.
type planState struct {
	reading, failing, switched Source
	isReading, isFailing       bool
	isSwitched                 bool
}

// classifyOne is d's connection, false when there is nothing to say: no
// plan source, or no evidence of use and nothing on.
func classifyOne(d providers.Descriptor, in Inputs, sources map[string]Source) (Connection, bool) {
	var ps planState
	plan := false
	for _, src := range d.Sources {
		if !src.PlanSource() {
			continue
		}
		plan = true
		s, ok := sources[src.Tag]
		if !ok {
			continue
		}
		switch {
		case s.Health != "failing" && !s.LastEventAt.IsZero() && in.Now.Sub(s.LastEventAt) <= Window:
			if !ps.isReading {
				ps.reading, ps.isReading = s, true
			}
		case s.Health == "failing" || (s.Switched && s.Health == "stale"):
			if !ps.isFailing {
				ps.failing, ps.isFailing = s, true
			}
		case s.Switched:
			if !ps.isSwitched {
				ps.switched, ps.isSwitched = s, true
			}
		}
	}
	if !plan {
		return Connection{}, false
	}
	id := string(d.ID)
	c := Connection{Provider: id, Display: d.DisplayName, Evidence: evidenceOf(id, in)}
	switch {
	case ps.isReading || in.Reported[id]:
		c.State, c.Source = Reading, ps.reading.Tag
		c.Summary = d.DisplayName + " — plan read"
	case ps.isFailing:
		c.State, c.Source = Failing, ps.failing.Tag
		c.Problem = problemOf(ps.failing, in.Now)
		c.Summary = d.DisplayName + " — plan not read: " + c.Problem
		if a, ok := d.ConnectFor(ps.failing.Tag); ok {
			c.Action = actionOf(id, a)
		}
	case ps.isSwitched:
		// A source on by default (Codex's app server) waits on every
		// machine; it is worth saying only where the tool is in use.
		if len(c.Evidence) == 0 {
			return Connection{}, false
		}
		c.State, c.Source = Connected, ps.switched.Tag
		c.Summary = d.DisplayName + " — connected, waiting for the first reading"
	default:
		a, ok := d.ConnectActionIf(func(src providers.Source) bool {
			return src.Credential != providers.AppLogin || in.SignIns[id]
		})
		if !ok {
			// Nothing else connects it: say the app login, as before.
			a, ok = d.ConnectAction()
		}
		if !ok || len(c.Evidence) == 0 {
			return Connection{}, false
		}
		c.State, c.Source, c.Action = NotConnected, a.Source, actionOf(id, a)
		c.Summary = d.DisplayName + " — " + seenWord(c.Evidence) + ", plan not read"
		if in.Dismissed[id] {
			c.State = Dismissed
			c.Summary += " (you said not to suggest it)"
		}
	}
	return c, true
}

// evidenceOf is what says the operator uses provider id: its recent turns,
// then its tool on this machine.
func evidenceOf(id string, in Inputs) []Evidence {
	var out []Evidence
	if u, ok := in.Use[id]; ok && u.Turns > 0 && !u.LastAt.IsZero() && in.Now.Sub(u.LastAt) <= Window {
		word := "turns"
		if u.Turns == 1 {
			word = "turn"
		}
		out = append(out, Evidence{Kind: Turns, Turns: u.Turns, At: u.LastAt,
			Detail: fmt.Sprintf("%d %s in the last %d days", u.Turns, word, int(Window/(24*time.Hour)))})
	}
	for _, what := range in.Installed[id] {
		out = append(out, Evidence{Kind: Installed, Detail: what})
	}
	return out
}

// seenWord says what the evidence shows: use, or only an install.
func seenWord(ev []Evidence) string {
	for _, e := range ev {
		if e.Kind == Turns {
			return "in use"
		}
	}
	return "installed"
}

// problemOf says what is wrong with a failing source.
func problemOf(s Source, now time.Time) string {
	if s.LastError != "" {
		return s.LastError
	}
	if s.LastEventAt.IsZero() {
		return "no reading yet"
	}
	return "no reading for " + age(now.Sub(s.LastEventAt))
}

// age is a duration in whole days, or hours under a day.
func age(d time.Duration) string {
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	return fmt.Sprintf("%dh", max(1, int(d/time.Hour)))
}

// actionOf is a registry action on the wire, with the docs and the way to
// stop being told.
func actionOf(id string, a providers.Action) *Action {
	return &Action{Source: a.Source, Command: a.Command, Needs: a.Needs, Where: strings.TrimSpace(a.Where),
		Docs: providers.DocsConnect, Dismiss: DismissCommand(id), Grant: a.Grant}
}

// DismissCommand is the command that stops TokenOps suggesting provider.
func DismissCommand(provider string) string { return "tokenops vendor-usage dismiss " + provider }

// Detected are the connections the operator has something to do for and
// has not dismissed: what a surface lists as "also in use".
func Detected(cs []Connection) []Connection {
	var out []Connection
	for _, c := range cs {
		if c.State == NotConnected {
			out = append(out, c)
		}
	}
	return out
}
