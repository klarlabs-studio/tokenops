package presence_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries/presence"
	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
	"go.klarlabs.de/tokenops/internal/infra/presence/presencetest"
)

func TestMain(m *testing.M) {
	presencetest.MaybeRunHelper()
	os.Exit(m.Run())
}

// The helper's contract, against the fake: enroll writes a blob and
// returns the public key, sign returns a DER signature over stdin that
// verifies under it.
func TestHelperEnrollsAndSigns(t *testing.T) {
	f := presencetest.Use(t)
	h, err := infrapresence.FindHelper()
	if err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(t.TempDir(), "key.blob")
	pub, err := h.Create(context.Background(), blob)
	if err != nil {
		t.Fatal(err)
	}
	k, err := domain.ParseKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	sig, err := h.Sign(context.Background(), blob, domain.EnrolmentMessage(k.ID, "mac", "me", at), "enrol\nthis\x07Mac")
	if err != nil {
		t.Fatal(err)
	}
	if !domain.VerifyEnrolment(k, "mac", "me", at, sig) {
		t.Fatal("the helper's signature does not verify")
	}
	calls := f.Calls()
	if len(calls) != 2 || calls[1] != "sign\tenrol this Mac" {
		t.Fatalf("calls %q: the dialog text must be one line without control characters", calls)
	}
}

func TestHelperExitCodesAreNamed(t *testing.T) {
	f := presencetest.Use(t)
	h, _ := infrapresence.FindHelper()
	for code, want := range map[int]error{
		presencetest.ExitNoGUI:     infrapresence.ErrNoGUI,
		presencetest.ExitLocked:    infrapresence.ErrLocked,
		presencetest.ExitCancelled: infrapresence.ErrCancelled,
	} {
		f.Fail(code)
		_, err := h.Sign(context.Background(), "blob", []byte("m"), "r")
		if !errors.Is(err, want) {
			t.Errorf("exit %d: %v, want %v", code, err, want)
		}
		if infrapresence.Unavailable(err) == errors.Is(want, infrapresence.ErrCancelled) {
			t.Errorf("exit %d: Unavailable=%v", code, infrapresence.Unavailable(err))
		}
	}
}

func TestHelperMissing(t *testing.T) {
	t.Setenv(infrapresence.HelperEnv, filepath.Join(t.TempDir(), "nope"))
	h, _ := infrapresence.FindHelper()
	if _, err := h.Sign(context.Background(), "b", nil, "r"); !errors.Is(err, infrapresence.ErrHelperMissing) {
		t.Fatalf("%v", err)
	}
	if _, err := (infrapresence.Helper{}).Create(context.Background(), "b"); !errors.Is(err, infrapresence.ErrHelperMissing) {
		t.Fatalf("%v", err)
	}
}

func TestDialogTextIsBounded(t *testing.T) {
	got := infrapresence.DialogText(strings.Repeat("a", 500))
	if r := []rune(got); len(r) != 240 || !strings.HasSuffix(got, "…") {
		t.Fatalf("len %d", len(r))
	}
}

// Install makes a created key the enrolled one; Trust then pins it, and a
// second install without a rotation is a replacement that is held.
func TestInstallPinsAndAReplacementIsHeld(t *testing.T) {
	presencetest.Use(t)
	h, _ := infrapresence.FindHelper()
	d := infrapresence.Dir(filepath.Join(t.TempDir(), "presence"))
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	install := func(rot *domain.Rotation) []byte {
		t.Helper()
		if err := d.Prepare(); err != nil {
			t.Fatal(err)
		}
		pub, err := h.Create(context.Background(), d.NewBlobPath())
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Install(pub, infrapresence.Enrolment{Key: domain.KeyID(pub), At: now}, rot); err != nil {
			t.Fatal(err)
		}
		return pub
	}
	pub := install(nil)
	if !d.HasBlob() {
		t.Fatal("no blob after install")
	}
	if st, _ := os.Stat(filepath.Join(string(d), infrapresence.PublicKeyFile)); st.Mode().Perm() != 0o600 {
		t.Fatalf("public.key mode %v", st.Mode())
	}
	tr, err := d.Trust(now)
	if err != nil || tr.State != domain.TrustPinned || tr.Key.ID != domain.KeyID(pub) {
		t.Fatalf("%+v %v", tr, err)
	}
	install(nil)
	if tr, _ := d.Trust(now.Add(time.Minute)); tr.State != domain.TrustHeld {
		t.Fatalf("a second key without a rotation is %s, want held", tr.State)
	}
}
