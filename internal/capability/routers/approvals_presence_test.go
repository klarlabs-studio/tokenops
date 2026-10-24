package routers

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// With boundary_approval: presence the router honours an upgrade approval
// only with the presence key's signature: one appended to the log by hand
// leaves the upgrade waiting; a signed one routes.
func TestApprovalGateNeedsASignedApprovalWhenEnforced(t *testing.T) {
	dir := t.TempDir()
	store, err := routingapproval.Open(filepath.Join(dir, "approvals.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := priv.PublicKey.Bytes()
	keys := infrapresence.Dir(filepath.Join(dir, "presence"))
	_ = keys.Prepare()
	if err := os.WriteFile(filepath.Join(string(keys), infrapresence.PublicKeyFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Trust(time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	var rc Config
	AttachApprovalGate(&rc, config.Config{BoundaryApproval: "presence"}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p := router.Proposal{Provider: eventschema.ProviderAnthropic, FromModel: "claude-sonnet-5", ProposedModel: "claude-opus-5"}
	rc.OnProposal(p)

	if err := store.Decide(p.Key(), string(router.DecisionApproved), p.ProposedModel); err != nil {
		t.Fatal(err)
	}
	if got := rc.UpgradeDecision(p.Provider, p.FromModel, p.ProposedModel); got != router.DecisionPending {
		t.Fatalf("unsigned approval: %q, want pending", got)
	}

	state, _ := store.Load()
	st := state[p.Key()]
	e := presence.RoutingExpect(p.Key(), st.RaisedAt, time.Now())
	proof := presence.Proof{V: presence.Version, Key: presence.KeyID(raw), Decision: p.Key(), Rule: presence.RoutingRule,
		Scope: e.Scope, Challenge: e.Challenge, Crossed: presence.CrossingsDigest(nil), Issued: time.Now().UTC().Truncate(time.Second)}
	msg, _ := proof.Message()
	sum := sha256.Sum256(msg)
	proof.Sig, _ = ecdsa.SignASN1(rand.Reader, priv, sum[:])
	if err := store.DecideSigned(p.Key(), string(router.DecisionApproved), p.ProposedModel, &proof); err != nil {
		t.Fatal(err)
	}
	if got := rc.UpgradeDecision(p.Provider, p.FromModel, p.ProposedModel); got != router.DecisionApproved {
		t.Fatalf("signed approval: %q, want approved", got)
	}
}
