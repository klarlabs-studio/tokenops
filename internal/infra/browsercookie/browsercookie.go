// Package browsercookie reads one cookie out of a local browser's own
// cookie store, so the operator never has to copy a session key out of
// developer tools and paste it into a terminal.
//
// The paste was the whole friction of the Claude usage meter: a value that
// must not be echoed, in a window the operator has to find, expiring every
// few weeks. Reading it where the browser already keeps it removes the step
// rather than making it safer.
//
// What it does NOT do: it reads exactly the cookie the caller names, for the
// host the caller names, from a profile on this machine. It never enumerates
// cookies, never returns anything else it saw, and never writes to the
// browser's store — a copy is read, so a running browser is fine.
//
// Chromium-family stores encrypt values with a key held in the login
// keychain; reading one makes macOS ask the operator to allow it, which is
// the consent step. Firefox stores them in the clear. Safari keeps its own
// binary format behind macOS privacy controls and is not supported.
package browsercookie

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1" //nolint:gosec // PBKDF2-SHA1 is the scheme Chromium's own code uses
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/pbkdf2"

	// Registers the sqlite driver used to read the cookie stores.
	_ "modernc.org/sqlite"
)

// ErrNotFound means the browser had no such cookie: the operator is not
// signed in there, or signed in under a different profile.
var ErrNotFound = errors.New("browsercookie: no such cookie in this browser")

// Browser is one installed browser's cookie store.
type Browser struct {
	// Name is what the operator calls it ("Chrome").
	Name string
	// dir is the profile-holding directory under $HOME.
	dir string
	// keychainService/keychainAccount identify the login-keychain entry
	// holding the value-encryption secret. Empty means unencrypted
	// (Firefox).
	keychainService string
	keychainAccount string
	// app is the application bundle, read for the browser's version so a
	// request can carry its User-Agent. Empty for Firefox.
	app string
	// firefox selects the Firefox profile layout and schema.
	firefox bool
}

// Known browsers, in the order they are tried.
var known = []Browser{
	{Name: "Chrome", dir: "Library/Application Support/Google/Chrome", keychainService: "Chrome Safe Storage", keychainAccount: "Chrome", app: "/Applications/Google Chrome.app"},
	{Name: "Arc", dir: "Library/Application Support/Arc/User Data", keychainService: "Arc Safe Storage", keychainAccount: "Arc", app: "/Applications/Arc.app"},
	{Name: "Brave", dir: "Library/Application Support/BraveSoftware/Brave-Browser", keychainService: "Brave Safe Storage", keychainAccount: "Brave", app: "/Applications/Brave Browser.app"},
	{Name: "Edge", dir: "Library/Application Support/Microsoft Edge", keychainService: "Microsoft Edge Safe Storage", keychainAccount: "Microsoft Edge", app: "/Applications/Microsoft Edge.app"},
	{Name: "Chromium", dir: "Library/Application Support/Chromium", keychainService: "Chromium Safe Storage", keychainAccount: "Chromium", app: "/Applications/Chromium.app"},
	{Name: "Firefox", dir: "Library/Application Support/Firefox/Profiles", firefox: true},
}

// Names lists the browsers this package can read, for help text.
func Names() []string {
	out := make([]string, 0, len(known))
	for _, b := range known {
		out = append(out, b.Name)
	}
	return out
}

// SecretFunc returns a browser's value-encryption secret. Production passes
// keychainSecret; tests pass their own.
type SecretFunc func(Browser) (string, error)

// DaemonKeychainWait bounds the keychain read where nobody is watching:
// the daemon polls unattended, and an unanswered prompt must not stall
// ingestion. InteractiveKeychainWait is for a command an operator just
// typed — they need time to find the dialog and read it, and cutting them
// off at fifteen seconds sends a working setup down the paste path.
const (
	DaemonKeychainWait      = 15 * time.Second
	InteractiveKeychainWait = 3 * time.Minute
)

// KeychainSecret returns a SecretFunc that reads the browser's secret from
// the login keychain, waiting at most wait for the operator to allow it.
func KeychainSecret(wait time.Duration) SecretFunc {
	return func(b Browser) (string, error) { return b.keychainSecretWithin(wait) }
}

// FindMany returns several cookies for host from ONE browser: the first
// that has the first name in names. Cloudflare's clearance cookie only
// works beside the session it was issued with, from the same browser, so
// they cannot be collected from wherever each happens to exist. Names it
// cannot find are simply absent from the result.
func FindMany(ctx context.Context, home, host string, names []string, only string, secret SecretFunc) (map[string]string, Browser, error) {
	if len(names) == 0 {
		return nil, Browser{}, errors.New("browsercookie: no cookie names given")
	}
	if secret == nil {
		secret = func(b Browser) (string, error) { return b.keychainSecret() }
	}
	var firstErr error
	tried := 0
	for _, b := range known {
		if only != "" && !strings.EqualFold(only, b.Name) {
			continue
		}
		root := filepath.Join(home, b.dir)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		tried++
		stores, err := b.stores(root)
		if err != nil {
			continue
		}
		for _, store := range stores {
			primary, err := b.read(ctx, store, host, names[0], secret)
			if err != nil {
				if !errors.Is(err, ErrNotFound) && firstErr == nil {
					firstErr = fmt.Errorf("%s: %w", b.Name, err)
				}
				continue
			}
			out := map[string]string{names[0]: primary}
			for _, n := range names[1:] {
				if v, err := b.read(ctx, store, host, n, secret); err == nil {
					out[n] = v
				}
			}
			return out, b, nil
		}
	}
	if firstErr != nil {
		return nil, Browser{}, firstErr
	}
	if tried == 0 {
		return nil, Browser{}, fmt.Errorf("browsercookie: no supported browser found (looked for %s)", strings.Join(Names(), ", "))
	}
	return nil, Browser{}, ErrNotFound
}

// read returns one cookie from one store.
func (b Browser) read(ctx context.Context, store, host, name string, secret SecretFunc) (string, error) {
	if b.firefox {
		return readFirefox(ctx, store, host, name)
	}
	return readChromium(ctx, store, func() (string, error) { return secret(b) }, host, name)
}

// UserAgent is the User-Agent this browser sends, or "" when it cannot be
// determined. Cloudflare binds its clearance cookie to the agent string it
// was issued to, so a request carrying that cookie has to match.
func (b Browser) UserAgent() string {
	if b.firefox || b.app == "" {
		return ""
	}
	out, err := exec.Command("plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-", //nolint:gosec // fixed argv, app path from the table above
		filepath.Join(b.app, "Contents", "Info.plist")).Output()
	if err != nil {
		return ""
	}
	return chromiumUserAgent(strings.TrimSpace(string(out)))
}

// chromiumUserAgent renders the agent string Chromium browsers send: the
// major version only, with the rest zeroed, as Chrome does.
func chromiumUserAgent(version string) string {
	major := version
	if i := strings.Index(version, "."); i > 0 {
		major = version[:i]
	}
	if major == "" {
		return ""
	}
	return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) " +
		"Chrome/" + major + ".0.0.0 Safari/537.36"
}

// Find returns the named cookie for host from the first browser that has
// it, and the browser it came from. Browsers that are not installed are
// skipped; a browser that is installed but has no such cookie is skipped
// too, so signing in elsewhere is not an error. only, when non-empty,
// limits the search to that browser name (case-insensitive).
func Find(ctx context.Context, home, host, name, only string) (string, Browser, error) {
	var firstErr error
	tried := 0
	for _, b := range known {
		if only != "" && !strings.EqualFold(only, b.Name) {
			continue
		}
		root := filepath.Join(home, b.dir)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		tried++
		v, err := b.find(ctx, root, host, name)
		switch {
		case err == nil:
			return v, b, nil
		case errors.Is(err, ErrNotFound):
			continue
		default:
			// A refused keychain prompt is worth reporting, but not
			// worth stopping the search for another browser that works.
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", b.Name, err)
			}
		}
	}
	if firstErr != nil {
		return "", Browser{}, firstErr
	}
	if tried == 0 {
		return "", Browser{}, fmt.Errorf("browsercookie: no supported browser found (looked for %s)", strings.Join(Names(), ", "))
	}
	return "", Browser{}, ErrNotFound
}

// find searches every profile directory of one browser.
func (b Browser) find(ctx context.Context, root, host, name string) (string, error) {
	stores, err := b.stores(root)
	if err != nil {
		return "", err
	}
	var firstErr error
	for _, store := range stores {
		var v string
		var err error
		if b.firefox {
			v, err = readFirefox(ctx, store, host, name)
		} else {
			v, err = readChromium(ctx, store, b.keychainSecret, host, name)
		}
		switch {
		case err == nil:
			return v, nil
		case errors.Is(err, ErrNotFound):
			continue
		default:
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		return "", firstErr
	}
	return "", ErrNotFound
}

// stores lists the cookie files of every profile under root.
func (b Browser) stores(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	file := "Cookies"
	if b.firefox {
		file = "cookies.sqlite"
	}
	var out []string
	// Chromium keeps the default profile directly under root.
	for _, candidate := range []string{filepath.Join(root, file), filepath.Join(root, "Default", file)} {
		if _, err := os.Stat(candidate); err == nil {
			out = append(out, candidate)
		}
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name(), file)
		if _, err := os.Stat(p); err == nil && !contains(out, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// keychainSecret asks the login keychain for this browser's value-encryption
// secret. macOS prompts the operator to allow it; that prompt is the consent
// step, and a refusal must read as a refusal rather than a missing cookie.
func (b Browser) keychainSecret() (string, error) {
	return b.keychainSecretWithin(DaemonKeychainWait)
}

func (b Browser) keychainSecretWithin(wait time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	out, err := exec.CommandContext(ctx, "security", "find-generic-password", //nolint:gosec // fixed argv, service/account from the table above
		"-w", "-s", b.keychainService, "-a", b.keychainAccount).Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("keychain prompt for %q went unanswered after %s — allow it (or 'Always Allow') and try again", b.keychainService, wait)
		}
		return "", fmt.Errorf("could not read %q (%w) — allow access when macOS asks, or fall back to the paste prompt", b.keychainService, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// readChromium returns one cookie value from a Chromium-family store.
func readChromium(ctx context.Context, path string, secret func() (string, error), host, name string) (string, error) {
	var plainValue string
	var enc []byte
	err := queryCookie(ctx, path,
		`SELECT value, encrypted_value FROM cookies WHERE name = ? AND host_key IN (?, ?) LIMIT 1`,
		name, host, &plainValue, &enc)
	if err != nil {
		return "", err
	}
	if plainValue != "" {
		return plainValue, nil
	}
	if len(enc) == 0 {
		return "", ErrNotFound
	}
	s, err := secret()
	if err != nil {
		// Naming the keychain matters: a refused prompt is the operator's
		// to retry, unlike a cookie that simply is not there.
		return "", fmt.Errorf("browsercookie: keychain secret unavailable: %w", err)
	}
	return decryptChromium(enc, deriveChromiumKey(s))
}

// readFirefox returns one cookie value from a Firefox store.
func readFirefox(ctx context.Context, path, host, name string) (string, error) {
	var value string
	var unused []byte
	err := queryCookie(ctx, path,
		`SELECT value, NULL FROM moz_cookies WHERE name = ? AND host IN (?, ?) LIMIT 1`,
		name, host, &value, &unused)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", ErrNotFound
	}
	return value, nil
}

// queryCookie runs q against a COPY of the store: the browser holds the
// original open, and nothing here may write to the operator's profile.
// The copy includes the write-ahead log, because a cookie written moments
// ago — a fresh sign-in, which is exactly when this runs — is still in it.
func queryCookie(ctx context.Context, path, q, name, host string, value *string, enc *[]byte) error {
	tmp, cleanup, err := copyStore(path)
	if err != nil {
		return err
	}
	defer cleanup()
	db, err := sql.Open("sqlite", "file:"+tmp+"?mode=ro")
	if err != nil {
		return fmt.Errorf("browsercookie: open store: %w", err)
	}
	defer func() { _ = db.Close() }()
	for _, h := range hostVariants(host) {
		row := db.QueryRowContext(ctx, q, name, h, h)
		var v sql.NullString
		var e []byte
		switch err := row.Scan(&v, &e); {
		case errors.Is(err, sql.ErrNoRows):
			continue
		case err != nil:
			return fmt.Errorf("browsercookie: read store: %w", err)
		}
		*value, *enc = v.String, e
		return nil
	}
	return ErrNotFound
}

// hostVariants covers how browsers store a domain versus how an operator
// names it: a cookie for claude.ai is stored under ".claude.ai".
func hostVariants(host string) []string {
	host = strings.TrimPrefix(host, ".")
	return []string{host, "." + host}
}

// copyStore copies the cookie store and its write-ahead sidecars into a
// temporary directory, keeping the base name so SQLite still pairs them.
func copyStore(path string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "tokenops-cookies-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	base := filepath.Base(path)
	dst := filepath.Join(dir, base)
	if err := copyFile(path, dst); err != nil {
		cleanup()
		return "", func() {}, err
	}
	// Absent sidecars are normal: the store may not be in WAL mode, or may
	// have been checkpointed. Only a failure to copy one that exists matters.
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err != nil {
			continue
		}
		if err := copyFile(path+suffix, dst+suffix); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return dst, cleanup, nil
}

func copyFile(from, to string) error {
	src, err := os.Open(from) //nolint:gosec // the caller's own browser profile
	if err != nil {
		return fmt.Errorf("browsercookie: open store: %w", err)
	}
	defer func() { _ = src.Close() }()
	dst, err := os.Create(to) //nolint:gosec // a temp file this process just made
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

// deriveChromiumKey turns the keychain secret into Chromium's AES key:
// PBKDF2-SHA1, the fixed salt and iteration count its own code uses.
func deriveChromiumKey(secret string) []byte {
	return pbkdf2.Key([]byte(secret), []byte("saltysalt"), 1003, 16, sha1.New)
}

// chromiumIV is Chromium's fixed IV: sixteen spaces.
func chromiumIV() []byte { return []byte("                ") }

// decryptChromium decrypts a v10 value. Recent versions prepend a 32-byte
// hash of the domain to the plaintext; it is dropped.
func decryptChromium(enc, key []byte) (string, error) {
	if len(enc) < 3 || string(enc[:3]) != "v10" {
		return "", errors.New("browsercookie: cookie is not in the expected v10 format")
	}
	body := enc[3:]
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	if len(body) == 0 || len(body)%block.BlockSize() != 0 {
		return "", errors.New("browsercookie: encrypted value has an unexpected length")
	}
	out := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, chromiumIV()).CryptBlocks(out, body)
	out, err = pkcs7Unpad(out, block.BlockSize())
	if err != nil {
		return "", err
	}
	if len(out) > 32 && !isPrintable(out[:32]) {
		out = out[32:]
	}
	return string(out), nil
}

func pkcs7Unpad(b []byte, size int) ([]byte, error) {
	if len(b) == 0 || len(b)%size != 0 {
		return nil, errors.New("browsercookie: bad padding")
	}
	n := int(b[len(b)-1])
	if n == 0 || n > size || n > len(b) {
		return nil, errors.New("browsercookie: bad padding")
	}
	return b[:len(b)-n], nil
}

// isPrintable reports whether b looks like text rather than a hash.
func isPrintable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
