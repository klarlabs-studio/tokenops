//go:build darwin

package keychain

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestQuietReadNeverPrompts reads an item that trusts no application from
// a keychain file of its own: any read needs the operator's permission,
// and the quiet read must answer ErrInteractionRequired instead of
// raising the Allow/Deny prompt. kSecUseAuthenticationUIFail alone did
// raise it on macOS 27.
//
// It creates and deletes a throwaway keychain (security create-keychain
// also adds it to the user's search list until it is deleted), so it runs
// only with TOKENOPS_KEYCHAIN_LIVE=1. If it ever shows a prompt, the item
// is named tokenops-keychain-test: deny it.
func TestQuietReadNeverPrompts(t *testing.T) {
	if os.Getenv("TOKENOPS_KEYCHAIN_LIVE") != "1" {
		t.Skip("set TOKENOPS_KEYCHAIN_LIVE=1 to create a throwaway keychain and check that a quiet read never prompts")
	}
	path := filepath.Join(t.TempDir(), "tokenops-test.keychain-db")
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("/usr/bin/security", args...).CombinedOutput(); err != nil {
			t.Fatalf("security %v: %v\n%s", args, err, out)
		}
	}
	run("create-keychain", "-p", "tokenops-test", path)
	t.Cleanup(func() { _ = exec.Command("/usr/bin/security", "delete-keychain", path).Run() })
	run("unlock-keychain", "-p", "tokenops-test", path)
	// -T "" trusts no application: every read needs permission.
	run("add-generic-password", "-a", "probe", "-s", "tokenops-keychain-test", "-w", "not-a-secret", "-T", "", path)

	testKeychainPath = path
	t.Cleanup(func() { testKeychainPath = "" })

	type answer struct {
		v   string
		err error
	}
	done := make(chan answer, 1)
	go func() {
		v, err := Quiet(Item{Service: "tokenops-keychain-test", Account: "probe"})
		done <- answer{v, err}
	}()
	select {
	case a := <-done:
		if !errors.Is(a.err, ErrInteractionRequired) || a.v != "" {
			t.Errorf("Quiet = %q, %v; want ErrInteractionRequired and nothing read", a.v, a.err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the quiet read waited 15s: macOS is showing a prompt for tokenops-keychain-test (deny it)")
	}
}
