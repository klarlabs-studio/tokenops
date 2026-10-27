package fireworks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Nexus CLI 0.10 keeps no minted-key.json and no ssoAccountId, but it
// knows who is signed in. The scan finds that email in its own files,
// in order: config.json, key-storage.json, then JSON under cli/.
func TestSignInEmailFindsTheSignedInEmail(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"config email", map[string]string{".nexus/config.json": `{"email":"ada@example.com","theme":"dark"}`}, "ada@example.com"},
		{"nested account.email", map[string]string{".nexus/config.json": `{"account":{"email":"ada@example.com"}}`}, "ada@example.com"},
		{"userEmail in key storage", map[string]string{".nexus/key-storage.json": `{"apiKey":"fw_k3y","userEmail":"ada@example.com"}`}, "ada@example.com"},
		{"cli session file", map[string]string{".nexus/cli/session.json": `{"signedInAs":"ada@example.com"}`}, "ada@example.com"},
		{"config wins over cli", map[string]string{
			".nexus/config.json":       `{"email":"ada@example.com"}`,
			".nexus/cli/session.json":  `{"email":"other@example.com"}`,
			".fireconnect/config.json": `{"email":"old@example.com"}`,
		}, "ada@example.com"},
		{"an email-named field wins", map[string]string{
			".nexus/config.json":      `{"user":"team@example.com"}`,
			".nexus/cli/session.json": `{"email":"ada@example.com"}`,
		}, "ada@example.com"},
		{"an unrelated address is not the user", map[string]string{".nexus/config.json": `{"support":"help@example.com"}`}, ""},
		{"fireconnect fallback", map[string]string{".fireconnect/config.json": `{"email":"ada@example.com"}`}, "ada@example.com"},
		{"nothing", map[string]string{".nexus/config.json": `{"theme":"dark"}`}, ""},
		{"not json", map[string]string{".nexus/config.json": `email = ada@example.com`}, ""},
		{"not an email", map[string]string{".nexus/config.json": `{"email":"ada"}`}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			for name, body := range c.files {
				writeFile(t, filepath.Join(home, name), body)
			}
			if got := SignInEmail(home); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// The scan reads files that hold the API key. It never returns a value
// that sits under a key-like field, nor one shaped like a Fireworks key,
// even when that value would pass for an email.
func TestSignInEmailNeverReturnsAKey(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".nexus/key-storage.json"), `{
		"apiKey": "fw_s3cret@example.com",
		"token": "ada@example.com",
		"secrets": {"value": "ada@example.com"},
		"credential": {"email": "ada@example.com"},
		"email": "fw_abc@example.com"
	}`)
	writeFile(t, filepath.Join(home, ".nexus/cli/keys.json"), `{"api_key":"bob@example.com","password":"bob@example.com"}`)
	if got := SignInEmail(home); got != "" {
		t.Errorf("returned %q from key fields", got)
	}
}

// Only small JSON files are read: a large one, a non-JSON one and one
// below a deep tree are skipped.
func TestSignInEmailIsBounded(t *testing.T) {
	home := t.TempDir()
	big := `{"email":"ada@example.com","pad":"` + strings.Repeat("x", maxScanBytes) + `"}`
	writeFile(t, filepath.Join(home, ".nexus/cli/big.json"), big)
	writeFile(t, filepath.Join(home, ".nexus/cli/notes.txt"), `{"email":"ada@example.com"}`)
	writeFile(t, filepath.Join(home, ".nexus/cli/a/b/c/d/deep.json"), `{"email":"ada@example.com"}`)
	if got := SignInEmail(home); got != "" {
		t.Errorf("read past the bounds: %q", got)
	}
}
