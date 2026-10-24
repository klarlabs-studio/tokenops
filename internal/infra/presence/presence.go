// Package presence is the presence key store (ADR 0015): the enrolled
// public key, a rotation the old key signed, and the pin that records which
// key approvals are checked against.
//
// Layout under ~/.tokenops/presence:
//
//	public.key      the enrolled key, raw X9.63 uncompressed P-256 (65 bytes)
//	rotation.json   the old key's signature over the new key, after a rotation
//	pin.json        the key approvals are checked against, and since when
//
// It only reads keys; enrolment (phase 4) writes public.key. It never
// touches the private key blob, and never asks for a signature.
package presence

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
)

// Files in the store.
const (
	PublicKeyFile = "public.key"
	RotationFile  = "rotation.json"
	PinFile       = "pin.json"
)

// maxFile bounds what is read from the store: a raw P-256 point is 65
// bytes, a pin or rotation a few hundred.
const maxFile = 8 << 10

// Dir is the store's directory.
type Dir string

// Trust reads the store and reconciles the key file with the pin
// (domain.Reconcile), writing the pin when it changed: a first key is
// pinned, a signed rotation moves the pin, an unsigned replacement starts
// its hold the first time it is seen.
//
// With no key file it reads nothing else: one failed open, which is every
// machine until phase 4's enrolment.
func (d Dir) Trust(now time.Time) (domain.Trust, error) {
	raw, err := readSmall(filepath.Join(string(d), PublicKeyFile))
	if errors.Is(err, os.ErrNotExist) {
		return domain.Trust{State: domain.TrustNotEnrolled}, nil
	}
	if err != nil {
		return domain.Trust{}, err
	}
	key, err := domain.ParseKey(raw)
	if err != nil {
		// A key file that is not a key proves nothing; treat it as a
		// replacement by an unknown key, so it is held and announced
		// rather than silently ignored.
		key = domain.Key{ID: domain.KeyID(raw), Raw: raw}
	}
	prev, err := d.pin()
	if err != nil {
		return domain.Trust{}, err
	}
	var rot *domain.Rotation
	if prev != nil && prev.KeyID != key.ID {
		rot = d.rotation()
	}
	trust, pin, changed := domain.Reconcile(&key, rot, prev, now)
	if changed {
		if err := d.putPin(pin); err != nil {
			return domain.Trust{}, err
		}
	}
	return trust, nil
}

func (d Dir) pin() (*domain.Pin, error) {
	b, err := readSmall(filepath.Join(string(d), PinFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p domain.Pin
	if json.Unmarshal(b, &p) != nil || p.KeyID == "" {
		// A pin that does not parse names no key the file can match, so
		// whatever key is there counts as a replacement and is held: a
		// corrupted pin must not read as a first enrolment.
		return &domain.Pin{KeyID: "unreadable"}, nil
	}
	return &p, nil
}

func (d Dir) rotation() *domain.Rotation {
	b, err := readSmall(filepath.Join(string(d), RotationFile))
	if err != nil {
		return nil
	}
	var r domain.Rotation
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return &r
}

func (d Dir) putPin(p domain.Pin) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(string(d), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(string(d), PinFile+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(string(d), PinFile))
}

func readSmall(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // TokenOps' own key store
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, maxFile))
}
