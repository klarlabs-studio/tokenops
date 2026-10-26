package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.klarlabs.de/vitra"
	"go.klarlabs.de/vitra/domain"
)

// findingsDaemon answers the findings routes, recording what it was sent.
type findingsDaemon struct {
	mu     sync.Mutex
	status int // 0 answers 200
	posts  []string
	bodies []map[string]any
}

func (f *findingsDaemon) start(t *testing.T) *daemon {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.posts = append(f.posts, r.Method+" "+r.URL.Path)
		f.bodies = append(f.bodies, in)
		if f.status != 0 {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(`{"error":"the finding has changed"}`))
			return
		}
		_, _ = w.Write([]byte(`{"acted":{"key":"boundary:headroom-pause","action":"boundary.promote","rule":"headroom-pause","stage":"enforce"},"findings":{"findings":[]}}`))
	}))
	t.Cleanup(srv.Close)
	d := &daemon{urlFile: filepath.Join(t.TempDir(), "daemon.url"), hc: srv.Client()}
	if err := os.WriteFile(d.urlFile, []byte(`{"url":"`+srv.URL+`","dashboard_token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestFindingInputsAreChecked(t *testing.T) {
	for _, in := range []findingActInput{
		{Key: "boundary:headroom-pause", Action: "boundary.promote"},
		{Key: "coach:tips-waited", Action: "tip.snooze"},
		{Key: "quota:openai:week-opus", Action: "tip.ack"},
	} {
		if !in.valid() {
			t.Errorf("%+v refused", in)
		}
	}
	for _, in := range []findingActInput{
		{Key: "", Action: "tip.ack"},
		{Key: "boundary:headroom-pause", Action: "boundary.demote"},
		{Key: "boundary:headroom-pause", Action: ""},
		{Key: "Boundary:Headroom", Action: "tip.ack"},
		{Key: "boundary:x; rm -rf ~", Action: "tip.ack"},
		{Key: "../../etc", Action: "tip.ack"},
		{Key: strings.Repeat("a", 129), Action: "tip.ack"},
	} {
		if in.valid() {
			t.Errorf("%+v accepted", in)
		}
	}
	for _, in := range []findingDismissInput{{Key: "waste:week", For: "day"}, {Key: "waste:week", For: "week"}, {For: "none"}, {Key: "waste:week", For: "none"}} {
		if !in.valid() {
			t.Errorf("%+v refused", in)
		}
	}
	for _, in := range []findingDismissInput{{Key: "waste:week", For: "month"}, {Key: "waste:week"}, {For: "day"}, {Key: "Waste Week", For: "day"}} {
		if in.valid() {
			t.Errorf("%+v accepted", in)
		}
	}
}

// A bad input never reaches the daemon.
func TestBadFindingInputsNeverReachTheDaemon(t *testing.T) {
	fd := &findingsDaemon{}
	m := &menubar{daemon: fd.start(t)}
	if err := m.actOnFinding(context.Background(), findingActInput{Key: "boundary:headroom-pause", Action: "boundary.demote"}); !errors.Is(err, errBadFinding) {
		t.Errorf("act = %v", err)
	}
	if err := m.dismissFinding(context.Background(), findingDismissInput{Key: "waste:week", For: "forever"}); !errors.Is(err, errBadFinding) {
		t.Errorf("dismiss = %v", err)
	}
	if len(fd.posts) != 0 {
		t.Fatalf("posted %v", fd.posts)
	}
}

func TestFindingActionsGoThroughTheDaemon(t *testing.T) {
	fd := &findingsDaemon{}
	m := &menubar{daemon: fd.start(t)}
	if err := m.actOnFinding(context.Background(), findingActInput{Key: "boundary:headroom-pause", Action: "boundary.promote"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.note, "headroom-pause now binds") || m.noteWarn {
		t.Errorf("note = %q warn %v", m.note, m.noteWarn)
	}
	if err := m.dismissFinding(context.Background(), findingDismissInput{Key: "waste:week", For: "week"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.note, "Hidden for a week") {
		t.Errorf("note = %q", m.note)
	}
	want := "POST /api/findings/act,POST /api/findings/dismiss"
	if got := strings.Join(fd.posts, ","); got != want {
		t.Fatalf("posted %s, want %s", got, want)
	}
	if fd.bodies[0]["key"] != "boundary:headroom-pause" || fd.bodies[0]["action"] != "boundary.promote" ||
		fd.bodies[1]["key"] != "waste:week" || fd.bodies[1]["for"] != "week" {
		t.Errorf("bodies = %v", fd.bodies)
	}
}

// A finding that changed since the panel read it is said in the note, not
// shown as a failure.
func TestStaleFindingIsANote(t *testing.T) {
	fd := &findingsDaemon{status: http.StatusConflict}
	m := &menubar{daemon: fd.start(t)}
	if err := m.actOnFinding(context.Background(), findingActInput{Key: "coach:tips-waited", Action: "tip.ack"}); err != nil {
		t.Fatal(err)
	}
	if m.note != staleNote || !m.noteWarn {
		t.Errorf("note = %q warn %v", m.note, m.noteWarn)
	}
}

// The panel's grant lets it call finding.act and finding.dismiss, and each
// checks its input before anything else runs.
func TestFindingCommandsAreWiredToThePanel(t *testing.T) {
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
	for _, tc := range []struct {
		command string
		input   map[string]any
	}{
		{"finding.act", map[string]any{"key": "boundary:headroom-pause", "action": "boundary.demote"}},
		{"finding.dismiss", map[string]any{"key": "waste:week", "for": "month"}},
	} {
		_, err := rt.Invoke(context.Background(), domain.InvocationRequest{Caller: panel, Command: domain.CommandName(tc.command), Input: tc.input})
		var denied *domain.ErrDenied
		if errors.As(err, &denied) || !errors.Is(err, errBadFinding) {
			t.Errorf("%s = %v, want the handler's input check", tc.command, err)
		}
	}
	other, _ := domain.NewCaller("other", domain.OriginPackagedLocal)
	_, err = rt.Invoke(context.Background(), domain.InvocationRequest{Caller: other, Command: "finding.act",
		Input: map[string]any{"key": "boundary:headroom-pause", "action": "boundary.promote"}})
	var denied *domain.ErrDenied
	if !errors.As(err, &denied) {
		t.Errorf("another window = %v, want a denial", err)
	}
	if len(fd.posts) != 0 {
		t.Fatalf("posted %v", fd.posts)
	}
}
