package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
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

// presenceHome is an isolated HOME with boundary_approval: presence and a
// software key enrolled where the daemon reads it. No dialog is raised.
func presenceHome(t *testing.T) (*Server, *ecdsa.PrivateKey) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	s, f := newActionServer(t, allowAll{})
	if err := os.WriteFile(f.path, []byte("boundary_approval: presence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := priv.PublicKey.Bytes()
	dir := infrapresence.Dir(filepath.Join(home, ".tokenops", "presence"))
	if err := dir.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(string(dir), infrapresence.PublicKeyFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := dir.Trust(time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	return s, priv
}

func signed(t *testing.T, priv *ecdsa.PrivateKey, p presence.Proof) map[string]any {
	t.Helper()
	raw, _ := priv.PublicKey.Bytes()
	p.V, p.Key, p.Issued = presence.Version, presence.KeyID(raw), time.Now().UTC().Truncate(time.Second)
	msg, err := p.Message()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(msg)
	sig, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"v": p.V, "key": p.Key, "decision": p.Decision, "rule": p.Rule, "session": p.Session, "scope": p.Scope,
		"challenge": p.Challenge, "crossed": p.Crossed, "issued": p.Issued, "sig": base64.StdEncoding.EncodeToString(sig),
	}
}

func body(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Validation step 3 (ADR 0015): with presence enforced, curl-style unsigned
// approvals to the daemon's boundary endpoint are refused, a deny is not,
// and a signed approval is taken.
func TestBoundaryDecisionEndpointNeedsAProof(t *testing.T) {
	s, priv := presenceHome(t)
	id := "bnd-0123456789ab"
	if err := boundaryledger.Dir("").PutPending(boundaryledger.Pending{
		ID: id, Rule: "session-spend", Require: "ask", Crossed: "session cost $25 >= $20", Action: "continue",
		Harness: "claude-code", SessionID: "s1", At: time.Now().UTC(), Challenge: "c0ffee",
	}); err != nil {
		t.Fatal(err)
	}
	rec := post(s, "/api/boundaries/decisions", "application/json", `{"id":"`+id+`","decision":"approve","session":true}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "an approval must come from a person") {
		t.Fatalf("unsigned approval = %d %s", rec.Code, rec.Body)
	}
	forged := signed(t, priv, presence.Proof{Decision: id, Rule: "session-spend", Session: "s1", Scope: "once",
		Challenge: "c0ffee", Crossed: presence.CrossingsDigest(nil)})
	rec = post(s, "/api/boundaries/decisions", "application/json",
		body(t, map[string]any{"id": id, "decision": "approve", "session": true, "presence_proof": forged}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "another scope") {
		t.Fatalf("proof for another scope = %d %s", rec.Code, rec.Body)
	}
	if _, ok, _ := boundaryledger.Dir("").GetAnswer(id, time.Now()); ok {
		t.Fatal("a refused approval wrote an answer")
	}
	good := signed(t, priv, presence.Proof{Decision: id, Rule: "session-spend", Session: "s1", Scope: "session",
		Challenge: "c0ffee", Crossed: presence.CrossingsDigest(nil)})
	rec = post(s, "/api/boundaries/decisions", "application/json",
		body(t, map[string]any{"id": id, "decision": "approve", "session": true, "presence_proof": good}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"presence":"signed"`) {
		t.Fatalf("signed approval = %d %s", rec.Code, rec.Body)
	}
	if rec := post(s, "/api/boundaries/decisions", "application/json", `{"id":"`+id+`","decision":"deny"}`); rec.Code != http.StatusOK {
		t.Fatalf("deny = %d %s", rec.Code, rec.Body)
	}
}

// Validation step 3: an unsigned routing upgrade approval is refused at the
// daemon's endpoint with presence enforced; a signed one is recorded with
// its proof.
func TestRoutingDecisionEndpointNeedsAProof(t *testing.T) {
	s, priv := presenceHome(t)
	path, err := routingapproval.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	store, err := routingapproval.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	key := "anthropic|claude-sonnet-4|claude-opus-4"
	raised := time.Now().UTC().Add(-time.Minute)
	if err := store.Propose(routingapproval.Record{Key: key, Provider: "anthropic", From: "claude-sonnet-4", To: "claude-opus-4", At: raised}); err != nil {
		t.Fatal(err)
	}
	rec := post(s, "/api/routing/decisions", "application/json", `{"key":"`+key+`","decision":"approve"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "carries no presence signature") {
		t.Fatalf("unsigned upgrade approval = %d %s", rec.Code, rec.Body)
	}
	proof := signed(t, priv, presence.Proof{Decision: key, Rule: presence.RoutingRule, Scope: "session",
		Challenge: presence.RoutingChallenge(key, raised), Crossed: presence.CrossingsDigest(nil)})
	rec = post(s, "/api/routing/decisions", "application/json", body(t, map[string]any{"key": key, "decision": "approve", "presence_proof": proof}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"presence":"signed"`) {
		t.Fatalf("signed upgrade approval = %d %s", rec.Code, rec.Body)
	}
	state, _ := store.Load()
	if st := state[key]; !st.Decided || st.Proof == nil {
		t.Fatalf("recorded %+v", st)
	}
}
