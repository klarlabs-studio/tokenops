package presence

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func genKey(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := priv.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return priv, raw
}

func write(t *testing.T, d Dir, name string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(string(d), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(string(d), name), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNoKeyIsNotEnrolledAndWritesNothing(t *testing.T) {
	d := Dir(filepath.Join(t.TempDir(), "presence"))
	tr, err := d.Trust(now)
	if err != nil || tr.State != domain.TrustNotEnrolled {
		t.Fatalf("%+v %v", tr, err)
	}
	if _, err := os.Stat(string(d)); !os.IsNotExist(err) {
		t.Fatalf("not-enrolled read created the store: %v", err)
	}
}

func TestFirstKeyIsPinnedThenAnUnsignedReplacementIsHeld(t *testing.T) {
	d := Dir(t.TempDir())
	_, raw := genKey(t)
	write(t, d, PublicKeyFile, raw)
	tr, err := d.Trust(now)
	if err != nil || tr.State != domain.TrustPinned || tr.Key.ID != domain.KeyID(raw) {
		t.Fatalf("%+v %v", tr, err)
	}
	_, other := genKey(t)
	write(t, d, PublicKeyFile, other)
	tr, err = d.Trust(now.Add(time.Hour))
	if err != nil || tr.State != domain.TrustHeld || tr.Pin.Previous != domain.KeyID(raw) {
		t.Fatalf("%+v %v", tr, err)
	}
	// The hold started when the replacement was first seen, and survives
	// a re-read.
	tr, _ = d.Trust(now.Add(time.Hour + 14*time.Minute))
	if tr.State != domain.TrustHeld {
		t.Fatalf("hold restarted or lost: %+v", tr)
	}
	tr, _ = d.Trust(now.Add(time.Hour + 15*time.Minute))
	if tr.State != domain.TrustPinned {
		t.Fatalf("hold did not end: %+v", tr)
	}
}

func TestSignedRotationHasNoHold(t *testing.T) {
	d := Dir(t.TempDir())
	oldPriv, oldRaw := genKey(t)
	write(t, d, PublicKeyFile, oldRaw)
	if _, err := d.Trust(now); err != nil {
		t.Fatal(err)
	}
	_, newRaw := genKey(t)
	from, to := domain.KeyID(oldRaw), domain.KeyID(newRaw)
	sum := sha256.Sum256(domain.RotationMessage(from, to, now))
	sig, err := ecdsa.SignASN1(rand.Reader, oldPriv, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(domain.Rotation{From: from, To: to, At: now, Sig: sig})
	write(t, d, RotationFile, b)
	write(t, d, PublicKeyFile, newRaw)
	tr, err := d.Trust(now.Add(time.Minute))
	if err != nil || tr.State != domain.TrustPinned || tr.Pin.How != domain.PinRotated || tr.Key.ID != to {
		t.Fatalf("%+v %v", tr, err)
	}
}

func TestCorruptPinHoldsTheKey(t *testing.T) {
	d := Dir(t.TempDir())
	_, raw := genKey(t)
	write(t, d, PublicKeyFile, raw)
	write(t, d, PinFile, []byte("{not json"))
	tr, err := d.Trust(now)
	if err != nil || tr.State != domain.TrustHeld {
		t.Fatalf("a corrupt pin let the key through without a hold: %+v %v", tr, err)
	}
}
