package keychain

import (
	"errors"
	"runtime"
	"testing"
)

// A quiet read of an item that does not exist is "not found", never a
// prompt and never a crash in the purego bridge.
func TestQuietReadOfAMissingItem(t *testing.T) {
	_, err := Quiet(Item{Service: "tokenops-test-item-that-does-not-exist", Account: "nobody"})
	if runtime.GOOS != "darwin" {
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("err = %v, want ErrUnsupported off macOS", err)
		}
		return
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// What `security find-internet-password -g` prints for Zed's sign-in:
// attributes on stdout, the password on stderr.
func TestParseSecurityLogin(t *testing.T) {
	stdout := "keychain: \"/Users/x/Library/Keychains/login.keychain-db\"\nversion: 512\nclass: \"inet\"\nattributes:\n" +
		"    \"acct\"<blob>=\"4242\"\n    \"srvr\"<blob>=\"https://zed.dev\"\n"
	got, err := parseSecurityLogin(stdout, "password: \"fixture-token\"\n")
	if err != nil || got.Account != "4242" || got.Secret != "fixture-token" {
		t.Errorf("got %+v, %v", got, err)
	}
	got, err = parseSecurityLogin(stdout, "password: 0x666978747572652D746F6B656E  \"fixture-token\"\n")
	if err != nil || got.Secret != "fixture-token" {
		t.Errorf("hex: %+v, %v", got, err)
	}
	if _, err := parseSecurityLogin("    \"acct\"<blob>=<NULL>\n", "password: \"x\"\n"); !errors.Is(err, ErrNotFound) {
		t.Errorf("no account: %v", err)
	}
}
