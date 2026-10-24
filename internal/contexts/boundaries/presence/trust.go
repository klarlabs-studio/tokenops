package presence

import (
	"bytes"
	"strings"
	"time"
)

// TrustState is whether approvals can be checked, and against what.
type TrustState string

const (
	// TrustNotEnrolled: no presence key on this machine, so nothing can be
	// proven and approvals rest on the channel they arrive on.
	TrustNotEnrolled TrustState = "not_enrolled"
	// TrustPinned: approvals are checked against the pinned key.
	TrustPinned TrustState = "pinned"
	// TrustHeld: the key was replaced without the old key's signature, and
	// approvals are refused until the hold ends.
	TrustHeld TrustState = "held"
)

// How the pinned key came to be pinned.
const (
	PinEnrolled = "enrolled"
	PinRotated  = "rotated"
	PinReplaced = "replaced"
)

// Pin is the record of which key approvals are checked against: the key
// file's key as last accepted. It is kept beside the key file and, from
// phase 4, in the daemon's store. It holds the public key itself, so a
// rotation can be checked against the old key once the file holds the new
// one.
type Pin struct {
	KeyID     string    `json:"key_id"`
	PublicKey []byte    `json:"public_key"`
	Since     time.Time `json:"since"`
	How       string    `json:"how"`
	// HoldUntil is when an unsigned replacement's hold ends.
	HoldUntil time.Time `json:"hold_until,omitzero"`
	// Previous is the key this one replaced or rotated from.
	Previous string `json:"previous,omitempty"`
}

// Rotation is the old key's signature over the new key: a key change the
// old key vouched for, so it needs no hold.
type Rotation struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	At   time.Time `json:"at"`
	Sig  []byte    `json:"sig"`
}

// RotationMessage is the exact bytes the old key signs for a rotation.
func RotationMessage(from, to string, at time.Time) []byte {
	var b strings.Builder
	b.WriteString(RotationDomain)
	writeField(&b, "from", from)
	writeField(&b, "to", to)
	writeField(&b, "at", at.UTC().Truncate(time.Second).Format(time.RFC3339))
	return []byte(b.String())
}

// Trust is what approvals are verified against.
type Trust struct {
	State TrustState
	Key   Key
	// ValidFrom is the earliest a proof may have been issued: when the key
	// was enrolled or rotated to, or when an unsigned replacement's hold
	// ends. A proof from before it was not made under this pin.
	ValidFrom time.Time
	Pin       Pin
}

// Reconcile decides which key is trusted, given the key in the key file
// (nil when there is none), any rotation statement beside it, and the pin
// recorded before (nil when none). It returns the trust and the pin to
// record; changed reports whether the pin must be written.
//
//   - No key file: not enrolled. The pin is kept, so a key that appears
//     later is a replacement, not a first enrolment.
//   - The pinned key: pinned, or held until an unsigned replacement's
//     hold ends.
//   - A first key, with no pin: enrolled now.
//   - A different key, with a rotation from the pinned key to it that the
//     pinned key signed: rotated, no hold.
//   - Any other different key: replaced, held for ReplacementHold from
//     the moment it was first seen, and proofs issued before the hold ends
//     are refused.
func Reconcile(file *Key, rot *Rotation, prev *Pin, now time.Time) (Trust, Pin, bool) {
	now = now.UTC()
	if file == nil {
		var keep Pin
		if prev != nil {
			keep = *prev
		}
		return Trust{State: TrustNotEnrolled, Pin: keep}, keep, false
	}
	if prev != nil && prev.KeyID == file.ID && bytes.Equal(prev.PublicKey, file.Raw) {
		return trustOf(*file, *prev, now), *prev, false
	}
	next := Pin{KeyID: file.ID, PublicKey: file.Raw, Since: now, How: PinEnrolled}
	if prev != nil && prev.KeyID != "" {
		next.Previous = prev.KeyID
		if rotatedBy(*prev, file.ID, rot) {
			next.How, next.Since = PinRotated, rot.At.UTC().Truncate(time.Second)
		} else {
			next.How, next.HoldUntil = PinReplaced, now.Add(ReplacementHold)
		}
	}
	return trustOf(*file, next, now), next, true
}

func trustOf(k Key, p Pin, now time.Time) Trust {
	t := Trust{State: TrustPinned, Key: k, ValidFrom: p.Since, Pin: p}
	if !p.HoldUntil.IsZero() {
		t.ValidFrom = p.HoldUntil
		if now.Before(p.HoldUntil) {
			t.State = TrustHeld
		}
	}
	return t
}

// rotatedBy reports whether rot moves the pinned key to the new key id
// and carries the pinned key's signature.
func rotatedBy(prev Pin, to string, rot *Rotation) bool {
	if rot == nil || rot.From != prev.KeyID || rot.To != to || rot.At.IsZero() {
		return false
	}
	old, err := ParseKey(prev.PublicKey)
	if err != nil || old.ID != prev.KeyID {
		return false
	}
	return old.verify(RotationMessage(rot.From, rot.To, rot.At), rot.Sig)
}
