package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The fake presence helper is this test binary, run again with
// fakeHelperEnv set: it never touches the Secure Enclave and never raises
// a dialog. It logs its arguments and stdin, exits with the code a test
// asks for, or "signs" by printing base64("sig:" + sha256(stdin)), which
// the fake daemon checks.
const (
	fakeHelperEnv  = "TOKENOPS_MENUBAR_FAKE_HELPER"
	fakeHelperExit = "TOKENOPS_MENUBAR_FAKE_HELPER_EXIT"
	fakeHelperLog  = "TOKENOPS_MENUBAR_FAKE_HELPER_LOG"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeHelperEnv) == "1" {
		os.Exit(runFakeHelper())
	}
	os.Exit(m.Run())
}

func runFakeHelper() int {
	in, _ := io.ReadAll(os.Stdin)
	rec, _ := json.Marshal(map[string]any{"args": os.Args[1:], "stdin": string(in)})
	if f, err := os.OpenFile(os.Getenv(fakeHelperLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		_, _ = f.Write(append(rec, '\n'))
		_ = f.Close()
	}
	if code, _ := strconv.Atoi(os.Getenv(fakeHelperExit)); code != 0 {
		return code
	}
	fmt.Println(fakeSig(in))
	return 0
}

func fakeSig(msg []byte) string {
	sum := sha256.Sum256(msg)
	return base64.StdEncoding.EncodeToString(append([]byte("sig:"), sum[:]...))
}

type helperCall struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
}

// fakeHelper points helperPath at the fake, failing with exit (0 signs).
func fakeHelper(t *testing.T, exit int) func() []helperCall {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "helper.log")
	t.Setenv(fakeHelperEnv, "1")
	t.Setenv(fakeHelperExit, strconv.Itoa(exit))
	t.Setenv(fakeHelperLog, log)
	prev := helperPath
	helperPath = func() (string, error) { return exe, nil }
	t.Cleanup(func() { helperPath = prev })
	return func() []helperCall {
		b, _ := os.ReadFile(log)
		var out []helperCall
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var c helperCall
			if json.Unmarshal([]byte(line), &c) == nil {
				out = append(out, c)
			}
		}
		return out
	}
}

// noTerminal fails the test if a terminal is opened.
func noTerminal(t *testing.T) {
	t.Helper()
	prev := openFile
	openFile = func(string) error { t.Error("opened a terminal"); return nil }
	t.Cleanup(func() { openFile = prev })
}

// terminal records whether a terminal was opened.
func terminal(t *testing.T) *bool {
	t.Helper()
	opened := false
	prev := openFile
	openFile = func(string) error { opened = true; return nil }
	t.Cleanup(func() { openFile = prev })
	return &opened
}

const (
	testID        = "bnd-0123456789ab"
	testStatement = "tokenops.approval/v1\nkey=19:pk-0123456789abcdef\n"
	testReason    = `approve TokenOps boundary "session-spend" (continue) in claude-code session s1: session cost $25 >= $20, for the next step only`
	testBlob      = "/Users/x/.tokenops/presence/key.blob"
)

// fakeDaemon answers the statement and decision routes as the daemon
// does, recording what the menu bar sent.
type fakeDaemon struct {
	mu        sync.Mutex
	statement int // status for /api/boundaries/statements; 0 answers it
	stmtReqs  []map[string]any
	decisions []map[string]any
}

func (f *fakeDaemon) start(t *testing.T) *daemon {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/api/boundaries/statements":
			f.stmtReqs = append(f.stmtReqs, in)
			switch f.statement {
			case 0:
			case http.StatusConflict:
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"no presence key is enrolled on this machine","code":"presence_unavailable"}`))
				return
			default:
				w.WriteHeader(f.statement)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"proof": map[string]any{"v": 1, "key": "pk-0123456789abcdef", "decision": testID, "rule": "session-spend",
					"session": "s1", "scope": "once", "challenge": "c0ffee", "crossed": "abc", "issued": "2026-10-24T10:00:00Z", "sig": nil},
				"statement": []byte(testStatement), "reason": testReason, "blob": testBlob, "key": "pk-0123456789abcdef",
			})
		case "/api/boundaries/decisions":
			f.decisions = append(f.decisions, in)
			_, _ = w.Write([]byte(`{"id":"` + testID + `","rule":"session-spend","decision":"approved","presence":"signed",` +
				`"presence_key":"pk-0123456789abcdef","note":"the agent may take its next step"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	d := &daemon{urlFile: filepath.Join(t.TempDir(), "daemon.url"), hc: srv.Client()}
	if err := os.WriteFile(d.urlFile, []byte(`{"url":"`+srv.URL+`","dashboard_token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return d
}

// With a key enrolled, Approve signs inline: the helper signs exactly the
// daemon's statement with exactly its reason and blob, and the daemon's
// proof goes back unchanged but for sig. No terminal opens.
func TestApproveSignsInlineAndPostsTheProof(t *testing.T) {
	calls := fakeHelper(t, 0)
	noTerminal(t)
	fd := &fakeDaemon{}
	m := &menubar{daemon: fd.start(t)}
	if err := m.approve(context.Background(), testID, false); err != nil {
		t.Fatal(err)
	}
	c := calls()
	if len(c) != 1 {
		t.Fatalf("helper calls %+v", c)
	}
	want := []string{"sign", "--blob", testBlob, "--reason", testReason}
	if strings.Join(c[0].Args, "\x00") != strings.Join(want, "\x00") || c[0].Stdin != testStatement {
		t.Fatalf("helper ran %q with stdin %q", c[0].Args, c[0].Stdin)
	}
	if len(fd.stmtReqs) != 1 || fd.stmtReqs[0]["id"] != testID || fd.stmtReqs[0]["session"] != false {
		t.Fatalf("statement requests %v", fd.stmtReqs)
	}
	if len(fd.decisions) != 1 {
		t.Fatalf("decisions %v", fd.decisions)
	}
	d := fd.decisions[0]
	proof, _ := d["presence_proof"].(map[string]any)
	if d["decision"] != "approve" || d["id"] != testID || proof == nil {
		t.Fatalf("posted %v", d)
	}
	if proof["sig"] != fakeSig([]byte(testStatement)) {
		t.Errorf("sig %v is not the helper's signature over the statement", proof["sig"])
	}
	for k, v := range map[string]any{"v": 1.0, "key": "pk-0123456789abcdef", "decision": testID, "rule": "session-spend",
		"session": "s1", "scope": "once", "challenge": "c0ffee", "crossed": "abc", "issued": "2026-10-24T10:00:00Z"} {
		if proof[k] != v {
			t.Errorf("proof[%s] = %v, the daemon sent %v", k, proof[k], v)
		}
	}
	if !strings.Contains(m.note, "Approved session-spend") || !strings.Contains(m.note, "pk-0123456789abcdef") || m.noteWarn {
		t.Errorf("note %q warn=%v", m.note, m.noteWarn)
	}
}

// Every way the helper declines is said in words, nothing is posted, and
// no terminal opens: the person was asked and did not approve, or could
// not be asked here.
func TestApproveSaysWhyTheHelperDidNotSign(t *testing.T) {
	for code, want := range map[int]string{
		exitCancelled:  "the dialog was cancelled",
		exitAuthFailed: "didn't match",
		exitLocked:     "the screen is locked",
		exitNoGUI:      "needs your screen",
		exitBadKey:     "enroll --replace",
		exitNoEnclave:  "no Secure Enclave",
		exitUsage:      "too old",
		1:              "exit 1",
	} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			fakeHelper(t, code)
			noTerminal(t)
			fd := &fakeDaemon{}
			m := &menubar{daemon: fd.start(t)}
			if err := m.approve(context.Background(), testID, false); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(m.note, want) || !strings.HasPrefix(m.note, "Not approved") || !m.noteWarn {
				t.Errorf("exit %d: note %q warn=%v, want %q", code, m.note, m.noteWarn, want)
			}
			if len(fd.decisions) != 0 {
				t.Errorf("exit %d posted %v", code, fd.decisions)
			}
		})
	}
}

// Where inline approval is not available the terminal asks, as before: no
// helper in the bundle, no key that can sign (409), or a daemon without
// the statement route. The helper never runs in those cases.
func TestApproveFallsBackToTheTerminal(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		missing bool
		want    string
	}{
		"helper missing":  {missing: true, want: "presence helper is not in this TokenOps.app"},
		"not enrolled":    {status: http.StatusConflict, want: "no presence key is enrolled"},
		"old daemon":      {status: http.StatusNotFound, want: "cannot prepare a signed approval"},
		"old daemon, 405": {status: http.StatusMethodNotAllowed, want: "cannot prepare a signed approval"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := fakeHelper(t, 0)
			if tc.missing {
				helperPath = func() (string, error) { return "", errHelperMissing }
			}
			opened := terminal(t)
			fd := &fakeDaemon{statement: tc.status}
			m := &menubar{daemon: fd.start(t)}
			if err := m.approve(context.Background(), testID, false); err != nil {
				t.Fatal(err)
			}
			if !*opened {
				t.Fatal("no terminal opened")
			}
			if !strings.Contains(m.note, "in the terminal that opened") || !strings.Contains(m.note, tc.want) || m.noteWarn {
				t.Errorf("note %q warn=%v", m.note, m.noteWarn)
			}
			if len(calls()) != 0 || len(fd.decisions) != 0 {
				t.Errorf("helper %v, posted %v", calls(), fd.decisions)
			}
		})
	}
}

// A helper path that does not exist is a fallback, not a failure.
func TestApproveWithAVanishedHelperFallsBack(t *testing.T) {
	prev := helperPath
	helperPath = func() (string, error) { return filepath.Join(t.TempDir(), helperName), nil }
	t.Cleanup(func() { helperPath = prev })
	opened := terminal(t)
	fd := &fakeDaemon{}
	m := &menubar{daemon: fd.start(t)}
	if err := m.approve(context.Background(), testID, false); err != nil {
		t.Fatal(err)
	}
	if !*opened || len(fd.decisions) != 0 {
		t.Fatalf("opened=%v posted %v", *opened, fd.decisions)
	}
}

// A bad id reaches neither the daemon, the helper nor a terminal.
func TestApproveRefusesWhatIsNotADecisionID(t *testing.T) {
	calls := fakeHelper(t, 0)
	noTerminal(t)
	fd := &fakeDaemon{}
	m := &menubar{daemon: fd.start(t)}
	if err := m.approve(context.Background(), "bnd-0123456789ab; rm -rf ~", false); err == nil {
		t.Fatal("accepted a bad id")
	}
	if len(calls()) != 0 || len(fd.stmtReqs) != 0 {
		t.Fatal("a bad id reached the helper or the daemon")
	}
}

// Deny is one click and never signs.
func TestDenyNeverRunsTheHelper(t *testing.T) {
	calls := fakeHelper(t, 0)
	fd := &fakeDaemon{}
	d := fd.start(t)
	if err := d.denyBoundary(context.Background(), testID, false); err != nil {
		t.Fatal(err)
	}
	if len(calls()) != 0 || len(fd.stmtReqs) != 0 {
		t.Fatal("deny asked for a signature")
	}
	if len(fd.decisions) != 1 || fd.decisions[0]["decision"] != "deny" || fd.decisions[0]["presence_proof"] != nil {
		t.Fatalf("posted %v", fd.decisions)
	}
}

// The helper is the one beside the app's binary.
func TestHelperPathIsBesideTheBinary(t *testing.T) {
	exe, _ := os.Executable()
	p, err := helperPath()
	if want := filepath.Join(filepath.Dir(exe), helperName); err == nil && p != want {
		t.Fatalf("helper %q, want %q", p, want)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(exe), helperName)); statErr != nil && err == nil {
		t.Fatal("found a helper that is not there")
	}
}
