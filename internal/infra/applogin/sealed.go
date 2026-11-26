package applogin

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1" // #nosec G505 -- Chromium's OSCrypt key derivation is PBKDF2-SHA1 by definition
	"encoding/base64"
	"errors"
	"strings"
)

// errSealed is a sealed value that does not open with the password given:
// wrong password, not this scheme, or damaged. It never carries the value.
var errSealed = errors.New("applogin: the sealed sign-in does not open with its Keychain password")

// chromiumSalt, chromiumIterations and chromiumKeyLen are Chromium's macOS
// OSCrypt parameters, which Electron apps such as Cursor use too (CodexBar's
// ChromiumSafeStorage.swift): PBKDF2-SHA1 of the Keychain password.
const (
	chromiumSalt       = "saltysalt"
	chromiumIterations = 1003
	chromiumKeyLen     = 16
)

// openSealed decrypts a value that starts with prefix: base64 of Chromium's
// "v10" ciphertext, AES-128-CBC under the key derived from password, with an
// IV of sixteen spaces and PKCS#7 padding. Nothing is logged or returned in
// an error; the plaintext is returned only when the padding checks out.
func openSealed(value, prefix, password string) (string, error) {
	body, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return "", errSealed
	}
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil || len(raw) <= 3 || !bytes.HasPrefix(raw, []byte("v10")) {
		return "", errSealed
	}
	ct := raw[3:]
	if len(ct)%aes.BlockSize != 0 {
		return "", errSealed
	}
	key, err := pbkdf2.Key(sha1.New, password, []byte(chromiumSalt), chromiumIterations, chromiumKeyLen)
	if err != nil {
		return "", errSealed
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", errSealed
	}
	iv := bytes.Repeat([]byte{' '}, aes.BlockSize)
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, ct)
	n := int(pt[len(pt)-1])
	if n < 1 || n > aes.BlockSize || !bytes.Equal(pt[len(pt)-n:], bytes.Repeat([]byte{byte(n)}, n)) {
		return "", errSealed
	}
	return string(pt[:len(pt)-n]), nil
}
