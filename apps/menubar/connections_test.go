package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.klarlabs.de/vitra"
	"go.klarlabs.de/vitra/domain"
)

var updateRows = flag.Bool("update-rows", false, "rewrite testdata/connection_rows.golden")

// cursorGlance is a daemon's glance on a machine where Cursor is in use and
// its plan is not read, beside Claude, whose plan is read.
const cursorGlance = `{"plan_headroom":{"reports":[{"provider":"anthropic"}]},"connections":[
 {"provider":"anthropic","display":"Claude","state":"reading","summary":"Claude — plan read","source":"claude-code-statusline"},
 {"provider":"cursor","display":"Cursor","state":"not_connected","summary":"Cursor — in use, plan not read",
  "evidence":[{"kind":"turns","detail":"41 turns in the last 7 days","turns":41,"at":"2026-11-24T10:00:00Z"},{"kind":"installed","detail":"/Applications/Cursor.app"}],
  "source":"cursor-web","action":{"source":"cursor-web","command":"TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor",
  "needs":"your cursor.com browser session (the WorkosCursorSessionToken cookie)",
  "where":"signed in at cursor.com: developer tools → Application → Cookies → WorkosCursorSessionToken",
  "docs":"https://github.com/klarlabs-studio/tokenops/blob/main/docs/reference/cli.md#connect-a-tool-tokenops-found",
  "dismiss":"tokenops vendor-usage dismiss cursor"}},
 {"provider":"factory","display":"Factory","state":"dismissed","summary":"Factory — installed, plan not read (you said not to suggest it)",
  "action":{"command":"tokenops vendor-usage setup factory","needs":"a Factory API key","docs":"https://example.com/elsewhere"}},
 {"provider":"amp","display":"Amp","state":"not_connected","summary":"Amp — installed, plan not read",
  "action":{"command":"tokenops vendor-usage setup amp","needs":"an Amp API key","docs":"https://example.com/elsewhere"}}
]}`

// The rows under the plans: only what is in use and not read, each with
// its command; a page outside TokenOps' docs is never offered.
func TestConnectionRows(t *testing.T) {
	got, err := json.MarshalIndent(connectionRows(json.RawMessage(cursorGlance)), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "connection_rows.golden")
	if *updateRows {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update-rows): %v", err)
	}
	if string(append(got, '\n')) != string(want) {
		t.Errorf("rows:\n%s\nwant:\n%s", got, want)
	}
	if rows := connectionRows(json.RawMessage(`{"plan_headroom":{}}`)); rows != nil {
		t.Errorf("a daemon from before connections: %v", rows)
	}
}

// Copy command copies the command Go holds for the row, never text the
// panel sends, and says what to put in place of the ….
func TestCopyConnectUsesTheRowGoHolds(t *testing.T) {
	var copied []string
	prev := copyText
	t.Cleanup(func() { copyText = prev })
	copyText = func(s string) error { copied = append(copied, s); return nil }
	m := &menubar{}
	m.last.Connections = connectionRows(json.RawMessage(cursorGlance))
	if err := m.copyConnect(connectionInput{Provider: "cursor"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"TOKENOPS_CURSOR_COOKIE=… tokenops vendor-usage enable cursor"}; !reflect.DeepEqual(copied, want) {
		t.Errorf("copied %q, want %q", copied, want)
	}
	if !strings.Contains(m.note, "in place of … your cursor.com browser session") || m.noteWarn {
		t.Errorf("note = %q", m.note)
	}
	for _, p := range []string{"factory", "claude", "../cursor", ""} {
		if err := m.copyConnect(connectionInput{Provider: p}); !errors.Is(err, errBadConnection) {
			t.Errorf("copy %q = %v, want refused", p, err)
		}
	}
	if len(copied) != 1 {
		t.Errorf("copied %d times", len(copied))
	}
}

// Don't show goes through the daemon, which writes and audits it; a tool
// the panel does not list never reaches it.
func TestDismissConnectionGoesThroughTheDaemon(t *testing.T) {
	fd := &findingsDaemon{}
	m := &menubar{daemon: fd.start(t)}
	m.last.Connections = connectionRows(json.RawMessage(cursorGlance))
	if err := m.dismissConnection(context.Background(), connectionInput{Provider: "nope"}); !errors.Is(err, errBadConnection) {
		t.Errorf("unlisted = %v", err)
	}
	if err := m.dismissConnection(context.Background(), connectionInput{Provider: "cursor"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fd.posts, ","); got != "POST /api/connections/dismiss" || fd.bodies[0]["provider"] != "cursor" {
		t.Errorf("posted %s %v", got, fd.bodies)
	}
	if !strings.HasPrefix(m.note, "Cursor won't be suggested again.") {
		t.Errorf("note = %q", m.note)
	}
}

// The panel's grant reaches the connection commands; another window does
// not.
func TestConnectionCommandsAreWiredToThePanel(t *testing.T) {
	rt, err := vitra.New(vitra.Config{AppID: appID})
	if err != nil {
		t.Fatal(err)
	}
	fd := &findingsDaemon{}
	m := &menubar{rt: rt, daemon: fd.start(t)}
	if err := m.register(); err != nil {
		t.Fatal(err)
	}
	for _, w := range []domain.WindowID{panelWindow, "other"} {
		if _, err := rt.OpenWindow(context.Background(), w, domain.OriginPackagedLocal); err != nil {
			t.Fatal(err)
		}
	}
	panel, _ := domain.NewCaller(panelWindow, domain.OriginPackagedLocal)
	for _, command := range []string{"connection.copy", "connection.docs", "connection.dismiss"} {
		_, err := rt.Invoke(context.Background(), domain.InvocationRequest{Caller: panel, Command: domain.CommandName(command),
			Input: map[string]any{"provider": "cursor"}})
		var denied *domain.ErrDenied
		if errors.As(err, &denied) || !errors.Is(err, errBadConnection) {
			t.Errorf("%s = %v, want the handler's check (nothing listed yet)", command, err)
		}
	}
	other, _ := domain.NewCaller("other", domain.OriginPackagedLocal)
	_, err = rt.Invoke(context.Background(), domain.InvocationRequest{Caller: other, Command: "connection.dismiss",
		Input: map[string]any{"provider": "cursor"}})
	var denied *domain.ErrDenied
	if !errors.As(err, &denied) {
		t.Errorf("another window = %v, want a denial", err)
	}
	if len(fd.posts) != 0 {
		t.Fatalf("posted %v", fd.posts)
	}
}

// The panel renders the rows muted under the plans and calls only the
// commands Go registers for them.
func TestConnectionRowsAreDrawnMuted(t *testing.T) {
	read := func(name string) string {
		b, err := fs.ReadFile(frontendFS, "frontend/"+name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	js, css := read("panel.js"), read("style.css")
	for _, want := range []string{"connectionsSection(main, v);", `"connection.copy"`, `"connection.docs"`, `"connection.dismiss"`, "Connect…", "Don't show"} {
		if !strings.Contains(js, want) {
			t.Errorf("panel.js lacks %s", want)
		}
	}
	if !strings.Contains(js, "connectionsSection(main, v);\n    coachFindings(main, v);") {
		t.Error("the rows come after the coach's findings; they belong under the plans")
	}
	if !strings.Contains(css, ".connections {") || !strings.Contains(css, "color: var(--muted)") {
		t.Error("style.css does not draw the rows muted")
	}
}
