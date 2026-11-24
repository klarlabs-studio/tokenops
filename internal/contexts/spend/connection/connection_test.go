package connection

import (
	"reflect"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

// Evidence of use and the state of each plan source decide what is said
// about a provider: whether its plan is read, whether the operator has
// something to do, and what.
func TestClassify(t *testing.T) {
	recent := Use{Turns: 41, LastAt: now.Add(-2 * time.Hour)}
	tests := []struct {
		name string
		in   Inputs
		// want is the provider's state; "" means it is not listed.
		want    State
		source  string
		command string
		problem string
		kinds   []EvidenceKind
	}{
		{
			name:    "used recently, nothing on: not connected, with the action",
			in:      Inputs{Use: map[string]Use{"cursor": recent}},
			want:    NotConnected,
			source:  "cursor-web",
			command: "TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor",
			kinds:   []EvidenceKind{Turns},
		},
		{
			name:    "installed only: not connected, said as installed",
			in:      Inputs{Installed: map[string][]string{"cursor": {"/Applications/Cursor.app"}}},
			want:    NotConnected,
			source:  "cursor-web",
			command: "TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor",
			kinds:   []EvidenceKind{Installed},
		},
		{
			name: "used and installed: both pieces of evidence, turns first",
			in: Inputs{Use: map[string]Use{"cursor": recent},
				Installed: map[string][]string{"cursor": {"/Applications/Cursor.app", "~/.cursor"}}},
			want:    NotConnected,
			source:  "cursor-web",
			command: "TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor",
			kinds:   []EvidenceKind{Turns, Installed, Installed},
		},
		{
			name: "turns older than the window are no evidence",
			in:   Inputs{Use: map[string]Use{"cursor": {Turns: 3, LastAt: now.Add(-8 * 24 * time.Hour)}}},
		},
		{
			name: "nothing seen: not listed",
			in:   Inputs{},
		},
		{
			name: "enabled and reading: reading",
			in: Inputs{Use: map[string]Use{"cursor": recent}, Sources: []Source{
				{Tag: "cursor-web", Switched: true, Health: "healthy", LastEventAt: now.Add(-3 * time.Minute)}}},
			want:   Reading,
			source: "cursor-web",
			kinds:  []EvidenceKind{Turns},
		},
		{
			name:  "a plan report reads it: reading",
			in:    Inputs{Use: map[string]Use{"cursor": recent}, Reported: map[string]bool{"cursor": true}},
			want:  Reading,
			kinds: []EvidenceKind{Turns},
		},
		{
			name: "enabled, refused: failing, with the reconnect",
			in: Inputs{Use: map[string]Use{"cursor": recent}, Sources: []Source{
				{Tag: "cursor-web", Switched: true, Health: "failing", LastError: "401 Unauthorized", LastEventAt: now.Add(-30 * time.Hour)}}},
			want:    Failing,
			source:  "cursor-web",
			command: "TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor",
			problem: "401 Unauthorized",
			kinds:   []EvidenceKind{Turns},
		},
		{
			name: "enabled, gone quiet: failing",
			in: Inputs{Installed: map[string][]string{"cursor": {"~/.cursor"}}, Sources: []Source{
				{Tag: "cursor-web", Switched: true, Health: "stale", LastEventAt: now.Add(-9 * 24 * time.Hour)}}},
			want:    Failing,
			source:  "cursor-web",
			command: "TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor",
			problem: "no reading for 9d",
			kinds:   []EvidenceKind{Installed},
		},
		{
			name: "enabled, no reading yet: connected",
			in: Inputs{Use: map[string]Use{"cursor": recent}, Sources: []Source{
				{Tag: "cursor-web", Switched: true, Health: "idle"}}},
			want:   Connected,
			source: "cursor-web",
			kinds:  []EvidenceKind{Turns},
		},
		{
			name: "on with nothing seen: not listed",
			in:   Inputs{Sources: []Source{{Tag: "cursor-web", Switched: true, Health: "idle"}}},
		},
		{
			name:    "dismissed: kept, never suggested",
			in:      Inputs{Use: map[string]Use{"cursor": recent}, Dismissed: map[string]bool{"cursor": true}},
			want:    Dismissed,
			source:  "cursor-web",
			command: "TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor",
			kinds:   []EvidenceKind{Turns},
		},
		{
			name: "dismissed but connected anyway: reading wins",
			in: Inputs{Use: map[string]Use{"cursor": recent}, Dismissed: map[string]bool{"cursor": true},
				Sources: []Source{{Tag: "cursor-web", Switched: true, Health: "healthy", LastEventAt: now.Add(-time.Minute)}}},
			want:   Reading,
			source: "cursor-web",
			kinds:  []EvidenceKind{Turns},
		},
		{
			name: "an always-on source that never read is not a connection",
			in: Inputs{Use: map[string]Use{"cursor": recent}, Sources: []Source{
				{Tag: "cursor-hook", Health: "healthy", LastEventAt: now.Add(-time.Minute)}}},
			want:    NotConnected,
			source:  "cursor-web",
			command: "TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor",
			kinds:   []EvidenceKind{Turns},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.Now = now
			got, ok := find(Classify(tt.in), "cursor")
			if tt.want == "" {
				if ok {
					t.Fatalf("listed as %+v, want not listed", got)
				}
				return
			}
			if !ok {
				t.Fatalf("not listed, want %s", tt.want)
			}
			if got.State != tt.want {
				t.Errorf("state = %s, want %s", got.State, tt.want)
			}
			if tt.source != "" && got.Source != tt.source {
				t.Errorf("source = %q, want %q", got.Source, tt.source)
			}
			switch {
			case tt.command == "" && got.Action != nil:
				t.Errorf("action = %+v, want none", got.Action)
			case tt.command != "" && (got.Action == nil || got.Action.Command != tt.command):
				t.Errorf("action = %+v, want command %q", got.Action, tt.command)
			}
			if got.Problem != tt.problem {
				t.Errorf("problem = %q, want %q", got.Problem, tt.problem)
			}
			var kinds []EvidenceKind
			for _, e := range got.Evidence {
				kinds = append(kinds, e.Kind)
			}
			if !reflect.DeepEqual(kinds, tt.kinds) {
				t.Errorf("evidence kinds = %v, want %v", kinds, tt.kinds)
			}
		})
	}
}

// The action carries everything a surface needs to say it: the command,
// the credential in plain words, where to find it, the docs, and how to
// stop being told.
func TestNotConnectedCarriesTheWholeAction(t *testing.T) {
	got, ok := find(Classify(Inputs{Now: now, Use: map[string]Use{"cursor": {Turns: 41, LastAt: now.Add(-2 * time.Hour)}}}), "cursor")
	if !ok || got.Action == nil {
		t.Fatalf("got %+v", got)
	}
	want := Connection{
		Provider: "cursor", Display: "Cursor", State: NotConnected,
		Summary:  "Cursor — in use, plan not read",
		Evidence: []Evidence{{Kind: Turns, Detail: "41 turns in the last 7 days", Turns: 41, At: now.Add(-2 * time.Hour)}},
		Source:   "cursor-web",
		Action: &Action{
			Source:  "cursor-web",
			Command: "TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor",
			Needs:   "your cursor.com browser session (the WorkosCursorSessionToken cookie)",
			Where:   "signed in at cursor.com: developer tools → Application → Cookies → WorkosCursorSessionToken",
			Docs:    "https://github.com/klarlabs-studio/tokenops/blob/main/docs/reference/cli.md#connect-a-tool-tokenops-found",
			Dismiss: "tokenops vendor-usage dismiss cursor",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v\naction %+v", got, want, *got.Action)
	}
}

// Every provider in the registry is classified, not just Cursor: one with
// an account reader, one read with the vendor's CLI by itself, one with
// no plan reader at all.
func TestClassifyCoversTheRegistry(t *testing.T) {
	used := Use{Turns: 5, LastAt: now.Add(-time.Hour)}
	got := Classify(Inputs{Now: now, Use: map[string]Use{
		"openrouter": used, // account reader with a key: setup
		"coderabbit": used, // its CLI reads by itself: nothing to do
		"gemini":     used, // no plan reader: nothing to connect
		"anthropic":  used, // the status line
	}})
	states := map[string]State{}
	for _, c := range got {
		states[c.Provider] = c.State
	}
	want := map[string]State{"openrouter": NotConnected, "anthropic": NotConnected}
	if !reflect.DeepEqual(states, want) {
		t.Errorf("states = %v, want %v", states, want)
	}
	if c, _ := find(got, "openrouter"); c.Action == nil || c.Action.Command != "tokenops vendor-usage setup openrouter" {
		t.Errorf("openrouter action = %+v", c.Action)
	}
}

// Ranked so a surface showing only the first few shows what matters: what
// fails, then what is in use and not read, busiest first.
func TestClassifyOrder(t *testing.T) {
	got := Classify(Inputs{Now: now,
		Use: map[string]Use{
			"openrouter": {Turns: 5, LastAt: now.Add(-time.Hour)},
			"cursor":     {Turns: 90, LastAt: now.Add(-time.Hour)},
			"github":     {Turns: 7, LastAt: now.Add(-time.Hour)},
		},
		Installed: map[string][]string{"factory": {"droid on PATH"}},
		Sources:   []Source{{Tag: "github-copilot", Switched: true, Health: "failing", LastError: "refused"}},
	})
	var order []string
	for _, c := range got {
		order = append(order, c.Provider+":"+string(c.State))
	}
	want := []string{"github:failing", "cursor:not_connected", "openrouter:not_connected", "factory:not_connected"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	if d := Detected(got); len(d) != 3 || d[0].Provider != "cursor" {
		t.Errorf("Detected = %v", d)
	}
}

func find(cs []Connection, provider string) (Connection, bool) {
	for _, c := range cs {
		if c.Provider == provider {
			return c, true
		}
	}
	return Connection{}, false
}
