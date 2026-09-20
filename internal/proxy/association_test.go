package proxy

import (
	"testing"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// An association nothing writes is a schema change, not a capability.
// The proxy already learns who is working from the session header, and
// `session:<id>` is exactly the actor form the task-ledger adapter
// produces — so events and reconstructed work name the same actor
// without a translation table between them.
func TestEventsFromASessionNameTheirActor(t *testing.T) {
	got := associationFor(&requestObservation{SessionID: "abc123"})

	if got.Actor != "session:abc123" {
		t.Errorf("actor = %q, want session:abc123", got.Actor)
	}
	// The proxy knows who, not what for. Inventing a work id here would
	// attribute every request to a goal nobody stated.
	if got.Work != "" || got.Execution != "" {
		t.Errorf("a work or execution was invented: %+v", got)
	}
}

// A request with no session header is unassociated, rather than
// attributed to an empty actor. "We do not know who" and "nobody" are
// different claims, and the second one is never true.
func TestEventsWithoutASessionAreUnassociated(t *testing.T) {
	got := associationFor(&requestObservation{})
	if !got.Empty() {
		t.Errorf("association = %+v, want empty", got)
	}
}

// The end-to-end path: what the observer emits is what reaches the
// store, so the association has to be on the envelope, not only in a
// helper.
func TestEmittedEnvelopesCarryTheAssociation(t *testing.T) {
	base, bus := startProxyWithTokenizer(t, echoUpstream(t), nil)
	postChatAs(t, base, "sess-42")

	events := waitForEvent(t, bus, 1)
	var seen bool
	for _, e := range events {
		if _, ok := e.Payload.(*eventschema.PromptEvent); !ok {
			continue
		}
		seen = true
		if e.Association.Actor != "session:sess-42" {
			t.Errorf("envelope actor = %q, want session:sess-42", e.Association.Actor)
		}
		if !e.Associated() {
			t.Error("an envelope with a session reports itself unassociated")
		}
	}
	if !seen {
		t.Fatal("no prompt event was emitted")
	}
}
