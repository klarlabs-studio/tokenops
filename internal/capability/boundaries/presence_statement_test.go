package boundaries

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
)

// The menu bar's path (ADR 0015 §3): the statement is prepared here, the
// helper signs exactly its bytes with exactly its reason, and the proof,
// posted over the API, is honoured as signed.
func TestPreparedStatementSignedByTheHelperIsHonouredOverTheAPI(t *testing.T) {
	env := enforcedEnv(t, presence.ModePresence, false)
	s, f := fakeSigner(t)
	enrolled := enrolFake(t, env, s)
	id := raise(t, env)

	st, err := PrepareApproval(env, id, true)
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := st.Proof.Message()
	if !bytes.Equal(msg, st.Statement) || st.Key != enrolled.KeyID || st.Blob != env.presenceDir().BlobPath() {
		t.Fatalf("statement %+v", st)
	}
	proof := st.Proof
	if proof.Sig, err = s.Sign(context.Background(), st.Blob, st.Statement, st.Reason); err != nil {
		t.Fatal(err)
	}
	calls := f.Calls()
	if got := calls[len(calls)-1]; got != "sign\t"+st.Reason {
		t.Fatalf("the helper showed %q, the statement's reason is %q", got, st.Reason)
	}
	d, err := Decide(env, DecideRequest{ID: id, Decision: "approve", Session: true, Proof: &proof, Method: MethodAPI})
	if err != nil || d.Presence != PresenceSigned {
		t.Fatalf("decided %+v %v", d, err)
	}
}

// No key that can sign here is ErrPresenceUnavailable, which a surface
// takes as "approve in the terminal instead"; a decision that is not
// waiting is an input error.
func TestPrepareApprovalRefusals(t *testing.T) {
	env := enforcedEnv(t, presence.ModePresence, false)
	id := raise(t, env)
	if _, err := PrepareApproval(env, id, false); !errors.Is(err, ErrPresenceUnavailable) {
		t.Fatalf("not enrolled: %v", err)
	}
	s, _ := fakeSigner(t)
	enrolFake(t, env, s)
	if _, err := PrepareApproval(env, "bnd-ffffffffffff", false); err == nil || !strings.Contains(err.Error(), "is waiting on an answer") {
		t.Fatalf("unknown decision: %v", err)
	}
}
