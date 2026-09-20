package browsercookie

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// chromiumFixture writes a Chrome-shaped cookie store holding one encrypted
// cookie, encrypted exactly as Chrome does on macOS: AES-128-CBC under a key
// derived from the Keychain secret, with a "v10" prefix.
func chromiumFixture(t *testing.T, secret, host, name, value string, domainPrefixed bool) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Cookies")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE cookies (host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB)`); err != nil {
		t.Fatal(err)
	}
	key := deriveChromiumKey(secret)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(value)
	if domainPrefixed {
		// Recent Chrome prepends a 32-byte hash of the domain.
		plain = append(make([]byte, 32), plain...)
	}
	padded := pkcs7Pad(plain, block.BlockSize())
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, chromiumIV()).CryptBlocks(out, padded)
	if _, err := db.Exec(`INSERT INTO cookies (host_key, name, value, encrypted_value) VALUES (?, ?, '', ?)`,
		host, name, append([]byte("v10"), out...)); err != nil {
		t.Fatal(err)
	}
	return path
}

func pkcs7Pad(b []byte, size int) []byte {
	n := size - len(b)%size
	for range n {
		b = append(b, byte(n))
	}
	return b
}

func TestChromiumCookieDecryptsTheStoredValue(t *testing.T) {
	const secret, want = "keychain-secret", "sk-ant-sid-abc123"
	path := chromiumFixture(t, secret, ".claude.ai", "sessionKey", want, false)

	// Chrome stores the cookie under ".claude.ai"; callers name the site.
	got, err := readChromium(context.Background(), path, func() (string, error) { return secret, nil }, "claude.ai", "sessionKey")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != want {
		t.Errorf("value = %q, want %q", got, want)
	}
}

// Recent Chrome prepends a 32-byte domain hash to the plaintext. The value
// after it is the cookie; returning the hash would send Anthropic nonsense.
func TestChromiumCookieStripsTheDomainHashPrefix(t *testing.T) {
	const secret, want = "keychain-secret", "sk-ant-sid-xyz789"
	path := chromiumFixture(t, secret, ".claude.ai", "sessionKey", want, true)

	got, err := readChromium(context.Background(), path, func() (string, error) { return secret, nil }, ".claude.ai", "sessionKey")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != want {
		t.Errorf("value = %q, want %q", got, want)
	}
}

// The cookie store is Chrome's, open and locked while it runs. Reading must
// not need Chrome closed, and must never write to it.
func TestChromiumCookieLeavesTheStoreUntouched(t *testing.T) {
	const secret = "keychain-secret"
	path := chromiumFixture(t, secret, ".claude.ai", "sessionKey", "sk-ant-sid-abc", false)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readChromium(context.Background(), path, func() (string, error) { return secret, nil }, ".claude.ai", "sessionKey"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Error("the browser's cookie store was modified")
	}
	// SQLite writes -wal/-shm beside a store it opens for writing; the
	// profile directory must come back exactly as it was.
	for _, sidecar := range []string{path + "-wal", path + "-shm", path + "-journal"} {
		if _, err := os.Stat(sidecar); err == nil {
			t.Errorf("left %s in the browser profile", filepath.Base(sidecar))
		}
	}
}

func TestChromiumCookieMissingIsNotFound(t *testing.T) {
	path := chromiumFixture(t, "s", ".example.com", "other", "v", false)
	_, err := readChromium(context.Background(), path, func() (string, error) { return "s", nil }, ".claude.ai", "sessionKey")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// A refused Keychain prompt must say so plainly: it is the one step that
// needs the operator, and "not found" would send them hunting elsewhere.
func TestChromiumCookieReportsARefusedKeychain(t *testing.T) {
	path := chromiumFixture(t, "s", ".claude.ai", "sessionKey", "v", false)
	_, err := readChromium(context.Background(), path, func() (string, error) {
		return "", errors.New("exit status 128: User canceled the operation")
	}, ".claude.ai", "sessionKey")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want the keychain failure", err)
	}
	if !strings.Contains(err.Error(), "keychain") {
		t.Errorf("err = %v, want it to name the keychain", err)
	}
}

func firefoxFixture(t *testing.T, host, name, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cookies.sqlite")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE moz_cookies (host TEXT, name TEXT, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO moz_cookies (host, name, value) VALUES (?, ?, ?)`, host, name, value); err != nil {
		t.Fatal(err)
	}
	return path
}

// Firefox stores cookies in the clear, so no keychain prompt is involved.
func TestFirefoxCookieReadsPlainValue(t *testing.T) {
	path := firefoxFixture(t, ".claude.ai", "sessionKey", "sk-ant-sid-firefox")
	got, err := readFirefox(context.Background(), path, ".claude.ai", "sessionKey")
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-ant-sid-firefox" {
		t.Errorf("value = %q", got)
	}
}

// Host matching has to accept both how browsers store the domain
// (".claude.ai") and how an operator names it ("claude.ai").
func TestHostVariants(t *testing.T) {
	got := hostVariants("claude.ai")
	for _, want := range []string{"claude.ai", ".claude.ai"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("variants %v missing %q", got, want)
		}
	}
}

// The browser is running while this reads: it holds its cookie store open,
// and a busy store must not mean "no cookie, go and paste it". Reading a
// copy is what makes a locked store readable, so this pins the copy.
func TestChromiumCookieReadsWhileTheBrowserHoldsTheStore(t *testing.T) {
	const secret, want = "keychain-secret", "sk-ant-sid-locked"
	path := chromiumFixture(t, secret, ".claude.ai", "sessionKey", want, false)

	holder, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	tx, err := holder.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO cookies (host_key, name, value, encrypted_value) VALUES ('x','y','z',NULL)`); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	got, err := readChromium(context.Background(), path, func() (string, error) { return secret, nil }, "claude.ai", "sessionKey")
	if err != nil {
		t.Fatalf("read while the store was held: %v", err)
	}
	if got != want {
		t.Errorf("value = %q, want %q", got, want)
	}
}

// Chrome's store uses a write-ahead log, so a cookie written moments ago —
// a fresh sign-in, which is when this runs — is still in the -wal sidecar
// and not yet in the main file. Copying only the main file would read a
// store without it.
func TestChromiumCookieReadsAValueStillInTheWriteAheadLog(t *testing.T) {
	const secret, want = "keychain-secret", "sk-ant-sid-inwal"
	path := chromiumFixture(t, secret, ".example.com", "other", "x", false)

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	enc := encryptChromiumValue(t, secret, want)
	if _, err := db.Exec(`INSERT INTO cookies (host_key, name, value, encrypted_value) VALUES ('.claude.ai','sessionKey','',?)`, enc); err != nil {
		t.Fatal(err)
	}
	// Keep the connection open so the write stays in the -wal, as it would
	// be while the browser runs.
	defer func() { _ = db.Close() }()
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Skipf("no -wal sidecar on this platform: %v", err)
	}

	got, err := readChromium(context.Background(), path, func() (string, error) { return secret, nil }, "claude.ai", "sessionKey")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != want {
		t.Errorf("value = %q, want %q — the write-ahead log was not read", got, want)
	}
}

// encryptChromiumValue encrypts v the way Chrome does, for fixtures.
func encryptChromiumValue(t *testing.T, secret, v string) []byte {
	t.Helper()
	block, err := aes.NewCipher(deriveChromiumKey(secret))
	if err != nil {
		t.Fatal(err)
	}
	padded := pkcs7Pad([]byte(v), block.BlockSize())
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, chromiumIV()).CryptBlocks(out, padded)
	return append([]byte("v10"), out...)
}

// Cloudflare's clearance cookie only works beside the session it was
// issued with, from the same browser: they have to be read together.
func TestFindManyReturnsCookiesFromOneBrowser(t *testing.T) {
	const secret = "keychain-secret"
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	profile := filepath.Join(home, "Library/Application Support/Google/Chrome/Default")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	store := chromiumFixture(t, secret, ".claude.ai", "sessionKey", "sk-ant-sid-both", false)
	if err := os.Rename(store, filepath.Join(profile, "Cookies")); err != nil {
		t.Fatal(err)
	}
	addChromiumCookie(t, filepath.Join(profile, "Cookies"), secret, ".claude.ai", "cf_clearance", "clearance-value")

	got, browser, err := FindMany(context.Background(), home, "claude.ai",
		[]string{"sessionKey", "cf_clearance", "absent_cookie"}, "", func(Browser) (string, error) { return secret, nil })
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if browser.Name != "Chrome" {
		t.Errorf("browser = %q", browser.Name)
	}
	if got["sessionKey"] != "sk-ant-sid-both" || got["cf_clearance"] != "clearance-value" {
		t.Errorf("cookies = %v", redactValues(got))
	}
	if _, ok := got["absent_cookie"]; ok {
		t.Errorf("absent cookie reported as present: %v", redactValues(got))
	}
}

// redactValues keeps test failures from printing cookie values.
func redactValues(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = "<" + string(rune('0'+len(v)%10)) + " chars>"
	}
	return out
}

func addChromiumCookie(t *testing.T, path, secret, host, name, value string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO cookies (host_key, name, value, encrypted_value) VALUES (?, ?, '', ?)`,
		host, name, encryptChromiumValue(t, secret, value)); err != nil {
		t.Fatal(err)
	}
}

// The clearance cookie is bound to the User-Agent it was issued to, so the
// request has to carry that browser's own.
func TestUserAgentNamesTheBrowser(t *testing.T) {
	ua := chromiumUserAgent("141.0.7390.55")
	for _, want := range []string{"Mozilla/5.0", "Chrome/141.0.0.0", "Safari/537.36"} {
		if !strings.Contains(ua, want) {
			t.Errorf("user agent %q missing %q", ua, want)
		}
	}
	if strings.Contains(ua, "7390") {
		t.Errorf("user agent leaks the build number, which Chrome does not send: %q", ua)
	}
}

// The wait is not one number: unattended the daemon must not stall, but an
// operator who just typed the command needs time to find the dialog —
// fifteen seconds sent a working setup down the paste path.
func TestKeychainWaitsDifferPerCaller(t *testing.T) {
	if DaemonKeychainWait >= InteractiveKeychainWait {
		t.Errorf("daemon wait %s is not shorter than the interactive %s", DaemonKeychainWait, InteractiveKeychainWait)
	}
	if InteractiveKeychainWait < time.Minute {
		t.Errorf("interactive wait %s is too short to find a dialog in", InteractiveKeychainWait)
	}
	if KeychainSecret(time.Second) == nil {
		t.Error("KeychainSecret returned nothing to call")
	}
}
