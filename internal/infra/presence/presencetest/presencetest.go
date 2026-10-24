// Package presencetest is a fake presence helper for tests: it answers the
// helper's commands (enroll, sign) with a software P-256 key, so tests run
// the whole enrol-sign-verify path without the Secure Enclave and without
// raising a dialog. The verifier cannot tell the two apart (ADR 0015 §7),
// which is the point of a fake and the reason it lives only in tests.
//
// The fake is the test binary itself: a package's TestMain calls
// MaybeRunHelper first, and Use points the helper path at os.Args[0] with
// the environment that makes the next run act as the helper.
package presencetest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	infrapresence "go.klarlabs.de/tokenops/internal/infra/presence"
)

// Environment the fake reads.
const (
	envActive = "TOKENOPS_FAKE_PRESENCE"
	// envFail makes the fake exit with this code, as the real helper does
	// when nobody can be asked (4), the screen is locked (5) or the
	// person cancelled (6).
	envFail = "TOKENOPS_FAKE_PRESENCE_FAIL"
	// envLog is a file each command appends "<command>\t<reason>" to, so a
	// test can read the dialog text it would have shown.
	envLog = "TOKENOPS_FAKE_PRESENCE_LOG"
)

// Exit codes of the real helper, for Fail.
const (
	ExitNoGUI     = 4
	ExitLocked    = 5
	ExitCancelled = 6
)

// MaybeRunHelper acts as the helper and exits when this run is one; it
// returns at once otherwise. Call it first in TestMain.
func MaybeRunHelper() {
	if os.Getenv(envActive) != "1" || len(os.Args) < 2 {
		return
	}
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout))
}

// Fake is the fake helper a test installed.
type Fake struct {
	t   testing.TB
	log string
}

// Use makes FindHelper return the fake for the rest of the test.
func Use(t testing.TB) *Fake {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := &Fake{t: t, log: filepath.Join(t.TempDir(), "presence.log")}
	t.Setenv(infrapresence.HelperEnv, exe)
	t.Setenv(envActive, "1")
	t.Setenv(envLog, f.log)
	t.Setenv(envFail, "")
	return f
}

// Fail makes every later command exit with code (0 to succeed again).
func (f *Fake) Fail(code int) {
	f.t.Helper()
	v := ""
	if code != 0 {
		v = strconv.Itoa(code)
	}
	f.t.Setenv(envFail, v)
}

// Calls are the commands the fake answered, each "<command>\t<reason>".
func (f *Fake) Calls() []string {
	f.t.Helper()
	b, err := os.ReadFile(f.log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// Key reads the software key the fake keeps in a blob, for a test that
// signs something itself.
func Key(t testing.TB, blobPath string) *ecdsa.PrivateKey {
	t.Helper()
	priv, err := readBlob(blobPath)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func run(args []string, stdin io.Reader, stdout io.Writer) int {
	flags := map[string]string{}
	for i := 1; i+1 < len(args); i += 2 {
		flags[strings.TrimPrefix(args[i], "--")] = args[i+1]
	}
	logCall(args[0] + "\t" + flags["reason"])
	if code, _ := strconv.Atoi(os.Getenv(envFail)); code != 0 {
		return code
	}
	switch args[0] {
	case "enroll":
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return 1
		}
		d, _ := priv.Bytes()
		if os.WriteFile(flags["blob"], []byte(hex.EncodeToString(d)), 0o600) != nil {
			return 1
		}
		raw, _ := priv.PublicKey.Bytes()
		_, _ = fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(raw))
	case "sign":
		priv, err := readBlob(flags["blob"])
		if err != nil {
			return 8
		}
		msg, _ := io.ReadAll(stdin)
		sum := sha256.Sum256(msg)
		sig, err := ecdsa.SignASN1(rand.Reader, priv, sum[:])
		if err != nil {
			return 1
		}
		_, _ = fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(sig))
	default:
		return 2
	}
	return 0
}

func readBlob(path string) (*ecdsa.PrivateKey, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a test's own temp file
	if err != nil {
		return nil, err
	}
	d, err := hex.DecodeString(string(bytes.TrimSpace(b)))
	if err != nil {
		return nil, err
	}
	return ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
}

func logCall(line string) {
	path := os.Getenv(envLog)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // a test's own temp file
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(f, line)
	_ = f.Close()
}
