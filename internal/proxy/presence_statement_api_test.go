package proxy

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
)

// statementReply is the statement routes' answer, read as a surface reads
// it: the proof kept as raw JSON to post back, never rebuilt.
type statementReply struct {
	Proof     map[string]json.RawMessage `json:"proof"`
	Statement []byte                     `json:"statement"`
	Reason    string                     `json:"reason"`
	Blob      string                     `json:"blob"`
	Key       string                     `json:"key"`
}

// withBlob puts a key blob beside the enrolled key, as enrolment does; the
// software key in presenceHome signs in the test, not through it.
func withBlob(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	dir := infrapresence.Dir(filepath.Join(home, ".tokenops", "presence"))
	if err := os.WriteFile(dir.BlobPath(), []byte("blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir.BlobPath()
}

// signStatement signs the statement's bytes as the helper does (ECDSA
// P-256 over SHA-256) and sets sig on the proof, the one thing a surface
// adds.
func signStatement(t *testing.T, priv *ecdsa.PrivateKey, st statementReply) map[string]json.RawMessage {
	t.Helper()
	sum := sha256.Sum256(st.Statement)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(base64.StdEncoding.EncodeToString(sig))
	st.Proof["sig"] = b
	return st.Proof
}

func readStatement(t *testing.T, rec *httptest.ResponseRecorder) statementReply {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("statement = %d %s", rec.Code, rec.Body)
	}
	var st statementReply
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// The daemon builds the statement a menu bar approval signs: its bytes are
// the proof's canonical message, the dialog names the rule, the action and
// the session, and the proof, signed over exactly those bytes and posted
// back unchanged but for sig, is taken as a signed approval.
func TestBoundaryStatementIsWhatTheVerifierChecks(t *testing.T) {
	for _, session := range []bool{false, true} {
		s, priv := presenceHome(t)
		blob := withBlob(t)
		id := "bnd-0123456789ab"
		// Raised before challenges existed: the statement gives it one.
		if err := boundaryledger.Dir("").PutPending(boundaryledger.Pending{
			ID: id, Rule: "session-spend", Require: "ask", Crossed: "session cost $25 >= $20", Action: "continue",
			Harness: "claude-code", SessionID: "sess-1234567890", At: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		st := readStatement(t, post(s, "/api/boundaries/statements", "application/json",
			body(t, map[string]any{"id": id, "session": session})))

		var proof presence.Proof
		raw, _ := json.Marshal(st.Proof)
		if err := json.Unmarshal(raw, &proof); err != nil {
			t.Fatal(err)
		}
		msg, err := proof.Message()
		if err != nil || !bytes.Equal(msg, st.Statement) {
			t.Fatalf("statement %q is not the proof's message %q (%v)", st.Statement, msg, err)
		}
		p, _, _ := boundaryledger.Dir("").GetPending(id)
		if p.Challenge == "" || proof.Challenge != p.Challenge {
			t.Fatalf("challenge %q, pending %q", proof.Challenge, p.Challenge)
		}
		wantScope := "once"
		if session {
			wantScope = "session"
		}
		if proof.Scope != wantScope || proof.Decision != id || proof.Rule != "session-spend" || st.Blob != blob || st.Key != proof.Key {
			t.Fatalf("statement %+v blob %q key %q", proof, st.Blob, st.Key)
		}
		for _, want := range []string{`boundary "session-spend"`, "(continue)", "session sess-123", "session cost"} {
			if !strings.Contains(st.Reason, want) {
				t.Errorf("dialog %q lacks %q", st.Reason, want)
			}
		}

		rec := post(s, "/api/boundaries/decisions", "application/json", body(t, map[string]any{
			"id": id, "decision": "approve", "session": session, "presence_proof": signStatement(t, priv, st),
		}))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"presence":"signed"`) {
			t.Fatalf("session=%v: signed approval = %d %s", session, rec.Code, rec.Body)
		}
	}
}

// A decision that is not waiting, or not a decision, gets no statement; a
// machine with no key that can sign says so with a code the menu bar
// falls back to the terminal on.
func TestBoundaryStatementRefusals(t *testing.T) {
	s, _ := presenceHome(t)
	rec := post(s, "/api/boundaries/statements", "application/json", `{"id":"bnd-0123456789ab"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"presence_unavailable"`) ||
		!strings.Contains(rec.Body.String(), "key blob") {
		t.Fatalf("no blob = %d %s", rec.Code, rec.Body)
	}
	withBlob(t)
	for _, tc := range []struct{ body, want string }{
		{`{"id":"bnd-0123456789ab"}`, "is waiting on an answer"},
		{`{"id":"nope"}`, "not a boundary decision id"},
		{`{"id":"bnd-0123456789ab","extra":1}`, "unknown field"},
	} {
		rec := post(s, "/api/boundaries/statements", "application/json", tc.body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s = %d %s, want 400 %q", tc.body, rec.Code, rec.Body, tc.want)
		}
	}
	// An answered decision is no longer pending.
	ledger := boundaryledger.Dir("")
	id := "bnd-00000000000a"
	if err := ledger.PutPending(boundaryledger.Pending{ID: id, Rule: "r", Require: "ask", Action: "continue",
		SessionID: "s1", At: time.Now().UTC(), Challenge: "c"}); err != nil {
		t.Fatal(err)
	}
	if err := ledger.DeletePending(id); err != nil {
		t.Fatal(err)
	}
	if rec := post(s, "/api/boundaries/statements", "application/json", `{"id":"`+id+`"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("answered decision = %d %s", rec.Code, rec.Body)
	}

	// No key enrolled at all.
	t.Setenv("HOME", t.TempDir())
	rec = post(s, "/api/boundaries/statements", "application/json", `{"id":"bnd-0123456789ab"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "no presence key is enrolled") {
		t.Fatalf("not enrolled = %d %s", rec.Code, rec.Body)
	}
}

// The routing statement is what VerifyRoutingProof checks: signed over its
// bytes, the upgrade approval is recorded with its proof.
func TestRoutingStatementIsWhatTheVerifierChecks(t *testing.T) {
	s, priv := presenceHome(t)
	withBlob(t)
	path, err := routingapproval.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	store, err := routingapproval.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	key := "anthropic|claude-sonnet-4|claude-opus-4"
	if err := store.Propose(routingapproval.Record{Key: key, Provider: "anthropic", From: "claude-sonnet-4", To: "claude-opus-4",
		At: time.Now().UTC().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if rec := post(s, "/api/routing/statements", "application/json", `{"key":"anthropic|x|y"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown proposal = %d %s", rec.Code, rec.Body)
	}
	st := readStatement(t, post(s, "/api/routing/statements", "application/json", body(t, map[string]any{"key": key})))
	if !strings.Contains(st.Reason, "from claude-sonnet-4 to claude-opus-4") {
		t.Errorf("dialog %q", st.Reason)
	}
	rec := post(s, "/api/routing/decisions", "application/json", body(t, map[string]any{
		"key": key, "decision": "approve", "presence_proof": signStatement(t, priv, st),
	}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"presence":"signed"`) {
		t.Fatalf("signed upgrade approval = %d %s", rec.Code, rec.Body)
	}
}

// The statement routes are behind the API token like every write: no
// token, or the wrong one, is refused before the body is read.
func TestStatementRoutesNeedTheAPIToken(t *testing.T) {
	srv := New("127.0.0.1:0", WithDashAuth(BearerAuth(headerEquals("Bearer tok"))),
		WithActions(func() ActionDeps { return ActionDeps{} }))
	for _, path := range []string{"/api/boundaries/statements", "/api/routing/statements"} {
		for _, auth := range []string{"", "Bearer wrong"} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"id":"bnd-0123456789ab"}`))
			req.Header.Set("Content-Type", "application/json")
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			srv.dashAuth.Middleware(srv.apiMux()).ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("POST %s with %q = %d, want 401", path, auth, rec.Code)
			}
		}
	}
	if s, _ := newActionServer(t, nil); s.ServesAPI(http.MethodPost, "/api/boundaries/statements") {
		t.Fatal("the statement route is mounted without API auth")
	}
}
