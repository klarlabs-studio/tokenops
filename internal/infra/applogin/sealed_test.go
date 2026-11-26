package applogin

import (
	"errors"
	"strings"
	"testing"
)

// The vector was made independently of this code: PBKDF2-SHA1 of the test
// password with "saltysalt", 1003 iterations, 16 bytes (hashlib), then
// AES-128-CBC with an IV of 16 spaces and PKCS7 padding (openssl), behind
// the "v10" prefix and base64, under Cursor's sealed prefix.
const (
	sealedPrefix   = "cursor-safe-storage:v1:"
	sealedPassword = "test-only-keychain-password"
	sealedPlain    = "test-only-access-token-value"
	sealedVector   = "cursor-safe-storage:v1:djEwyY8LsigX2ceQSpFjSfTL/+V/HmIDNenHRtbJh9qhuwg="
)

func TestOpenSealedMatchesChromiumVector(t *testing.T) {
	got, err := openSealed(sealedVector, sealedPrefix, sealedPassword)
	if err != nil || got != sealedPlain {
		t.Fatalf("openSealed() = %q, %v; want %q", got, err, sealedPlain)
	}
}

func TestOpenSealedRefusesWhatItCannotOpen(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		password string
	}{
		{name: "wrong password", value: sealedVector, password: "not-the-password"},
		{name: "no prefix", value: strings.TrimPrefix(sealedVector, sealedPrefix), password: sealedPassword},
		{name: "not base64", value: sealedPrefix + "!!!", password: sealedPassword},
		{name: "not v10", value: sealedPrefix + "djExAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", password: sealedPassword},
		{name: "not whole blocks", value: sealedPrefix + "djEwAAAA", password: sealedPassword},
		{name: "no payload", value: sealedPrefix + "djEw", password: sealedPassword},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := openSealed(tt.value, sealedPrefix, tt.password)
			if err == nil {
				t.Fatalf("openSealed() = %q, want an error", got)
			}
			if !errors.Is(err, errSealed) {
				t.Errorf("error %v, want errSealed", err)
			}
			if strings.Contains(err.Error(), sealedPlain) || strings.Contains(err.Error(), tt.value) {
				t.Errorf("error carries the value: %v", err)
			}
		})
	}
}
