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
