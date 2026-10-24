package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bcap "go.klarlabs.de/tokenops/internal/capability/boundaries"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
	"go.klarlabs.de/tokenops/internal/infra/presence/presencetest"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// raisedDecision makes the enforced session-spend rule refuse in session
// s1 and returns the decision id the refusal names.
func raisedDecision(t *testing.T) string {
	t.Helper()
	t.Setenv("CLAUDE_CODE_SESSION_ATTENDED", "0")
	h := runBoundaryHook(t).HookSpecificOutput
	i := strings.Index(h.PermissionDecisionReason, "bnd-")
	if i < 0 {
		t.Fatalf("no decision id in %q", h.PermissionDecisionReason)
	}
	return strings.Trim(strings.Fields(h.PermissionDecisionReason[i:])[0], "`,.")
}

// The opted-in path end to end through the CLI, with the fake helper:
// enrol, enforce, approve (signed), and the pause point lets the agent on.
func TestPresenceEnrolEnforceApprove(t *testing.T) {
	home := boundaryHome(t, spendBoundary+"    stage: enforce\n", 25)
	f := presencetest.Use(t)
	if out, err := runCLI(t, nil, "boundaries", "presence", "enforce"); err == nil || !strings.Contains(err.Error(), "enrol one first") {
		t.Fatalf("enforce without a key = %q, %v", out, err)
	}
	out, err := runCLI(t, nil, "boundaries", "presence", "enroll")
	if err != nil || !strings.Contains(out, "enrolled") || !strings.Contains(out, filepath.Join(home, ".tokenops", "presence")) {
		t.Fatalf("enroll = %q, %v", out, err)
	}
	if out, err := runCLI(t, nil, "boundaries", "presence", "enforce"); err != nil || !strings.Contains(out, "boundary_approval: presence") {
		t.Fatalf("enforce = %q, %v", out, err)
	}
	out, err = runCLI(t, nil, "boundaries", "presence", "status", "--json")
	var st bcap.PresenceStatus
	if err != nil || json.Unmarshal([]byte(out), &st) != nil || st.State != bcap.PresenceStateEnforced || st.Approval != "presence" {
		t.Fatalf("status = %q, %v", out, err)
	}

	id := raisedDecision(t)
	atTerminal(t)
	out, err = runCLI(t, nil, "approve", id, "--json")
	if err != nil || !strings.Contains(out, `"presence": "signed"`) {
		t.Fatalf("approve = %q, %v", out, err)
	}
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "" {
		t.Fatalf("a signed approval did not let the agent on: %+v", d)
	}
	calls := f.Calls()
	if last := calls[len(calls)-1]; !strings.Contains(last, `boundary "session-spend" (continue)`) || !strings.Contains(last, "session s1") {
		t.Fatalf("dialog %q", last)
	}
}

// Validation step 3 (ADR 0015): `script -q /dev/null tokenops approve` gives
// the command a terminal, which atTerminal stands in for. With presence
// enforced it is still refused when no valid signature can be made: no
// helper, nobody at the screen, or a helper whose key is not the enrolled
// one. Denying needs no signature and asks no one.
func TestPresenceApproveWithoutASignatureIsRefused(t *testing.T) {
	boundaryHome(t, spendBoundary+"    stage: enforce\nboundary_approval: presence\n", 25)
	f := presencetest.Use(t)
	if _, err := runCLI(t, nil, "boundaries", "presence", "enroll"); err != nil {
		t.Fatal(err)
	}
	id := raisedDecision(t)
	atTerminal(t)

	f.Fail(presencetest.ExitNoGUI)
	if out, err := runCLI(t, nil, "approve", id); err == nil || !strings.Contains(err.Error(), "no one can be asked here") {
		t.Fatalf("no GUI = %q, %v", out, err)
	}
	f.Fail(presencetest.ExitCancelled)
	if out, err := runCLI(t, nil, "approve", id); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancelled = %q, %v", out, err)
	}
	f.Fail(0)
	// A key blob that is not the enrolled key's: what an agent's own
	// helper or a swapped blob would sign with.
	blob := filepath.Join(os.Getenv("HOME"), ".tokenops", "presence", infrapresence.BlobFile)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	d, _ := other.Bytes()
	if err := os.WriteFile(blob, []byte(hex.EncodeToString(d)), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLI(t, nil, "approve", id); err == nil || !strings.Contains(err.Error(), "signature does not verify") {
		t.Fatalf("foreign key = %q, %v", out, err)
	}
	t.Setenv(infrapresence.HelperEnv, filepath.Join(t.TempDir(), "missing"))
	if out, err := runCLI(t, nil, "approve", id); err == nil || !strings.Contains(err.Error(), "presence helper") {
		t.Fatalf("no helper = %q, %v", out, err)
	}
	if d := runBoundaryHook(t); d.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("an unsigned approval let the agent on: %+v", d)
	}
	before := len(f.Calls())
	if out, err := runCLI(t, nil, "deny", id); err != nil {
		t.Fatalf("deny = %q, %v", out, err)
	}
	if len(f.Calls()) != before {
		t.Fatal("deny asked the helper for a signature")
	}
}

// Validation step 3: a hand-written answer file is not honoured with
// presence enforced, and the decision records that it was not.
func TestPresenceHandWrittenAnswerFile(t *testing.T) {
	home := boundaryHome(t, spendBoundary+"    stage: enforce\nboundary_approval: presence\n", 25)
	presencetest.Use(t)
	if _, err := runCLI(t, nil, "boundaries", "presence", "enroll"); err != nil {
		t.Fatal(err)
	}
	id := raisedDecision(t)
	answers := filepath.Join(home, ".tokenops", "boundaries", "answers")
	if err := os.MkdirAll(answers, 0o700); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Format(time.RFC3339)
	body := `{"id":"` + id + `","rule":"session-spend","session_id":"s1","approved":true,"scope":"session","approver":"felix","method":"cli","at":"` + at + `"}`
	if err := os.WriteFile(filepath.Join(answers, id+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := boundaryledger.Dir(filepath.Join(home, ".tokenops", "boundaries")).GetAnswer(id, time.Now()); !ok {
		t.Fatal("the hand-written answer is not where the pause point reads answers")
	}
	d := runBoundaryHook(t).HookSpecificOutput
	if d.PermissionDecision != "deny" || !strings.Contains(d.PermissionDecisionReason, "not honoured") {
		t.Fatalf("hand-written answer = %+v", d)
	}
	var refused bool
	for _, e := range ledger(t, home) {
		if p, ok := e.Payload.(*eventschema.DecisionEvent); ok && p.Approval.Presence == bcap.PresenceRefused {
			refused = true
		}
	}
	if !refused {
		t.Fatal("the refused answer was not recorded")
	}
}

func TestPresenceIsCLIOnly(t *testing.T) {
	if _, ok := subCLIOnly["boundaries presence"]; !ok {
		t.Fatal("boundaries presence must be listed as CLI-only, with its reason")
	}
}
