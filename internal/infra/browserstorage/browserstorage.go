// Package browserstorage reads a few localStorage entries of one site from
// a local Chromium-family browser (Chrome, Arc, Brave, Edge, Chromium), for
// a vendor whose web session lives in localStorage rather than a cookie
// (Devin, Windsurf).
//
// It is called only by the interactive `tokenops vendor-usage setup <id>`,
// never by the daemon (ADR 0013). What it promises:
//
//   - It reads exactly the keys the caller names, for the origins the
//     caller names, and returns nothing else it saw.
//   - It never touches the browser's own files beyond reading them: the
//     profile's LevelDB directory is copied to a private temporary
//     directory (the running browser holds a lock on it), opened
//     read-only there, and the copy removed.
//   - It reads no Keychain item: Chromium does not encrypt localStorage.
//   - No error carries a value it read.
package browserstorage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"

	"go.klarlabs.de/tokenops/internal/infra/browsercookie"
)

// ErrNotFound is no browser profile holding the site's first key.
var ErrNotFound = errors.New("browserstorage: the site's session is not in any local browser")

// maxDB bounds what is copied of one profile's localStorage.
const maxDB = 512 << 20

// Found is a site's entries and where they were read.
type Found struct {
	// Values are the keys found, by name; a value stored as a JSON string
	// is unquoted.
	Values map[string]string
	// Browser and Origin say where.
	Browser, Origin string
}

// JSON is Values as one JSON object.
func (f Found) JSON() string {
	b, _ := json.Marshal(f.Values)
	return string(b)
}

// Find reads keys from the first profile, in browser order, that holds the
// first of keys for one of origins, trying origins in order within each
// profile; entries of different origins are never mixed. A key may be a
// pattern in which "*" matches any run of characters; the entries it
// matches are returned under their own names. only limits it to one
// browser by name.
func Find(home string, origins, keys []string, only string) (Found, error) {
	if len(keys) == 0 || len(origins) == 0 {
		return Found{}, errors.New("browserstorage: no origin or key named")
	}
	var firstErr error
	for _, root := range browsercookie.ChromiumRoots(home) {
		if only != "" && !strings.EqualFold(only, root.Name) {
			continue
		}
		for _, dir := range profileStores(root.Dir) {
			values, origin, err := readProfile(dir, origins, keys)
			switch {
			case err == nil:
				return Found{Values: values, Browser: root.Name, Origin: origin}, nil
			case errors.Is(err, ErrNotFound):
			default:
				if firstErr == nil {
					firstErr = fmt.Errorf("browserstorage: %s: %w", root.Name, err)
				}
			}
		}
	}
	if firstErr != nil {
		return Found{}, firstErr
	}
	return Found{}, ErrNotFound
}

// profileStores lists the localStorage LevelDB directories of every profile
// under root.
func profileStores(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	add := func(profile string) {
		p := filepath.Join(profile, "Local Storage", "leveldb")
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			for _, o := range out {
				if o == p {
					return
				}
			}
			out = append(out, p)
		}
	}
	add(filepath.Join(root, "Default"))
	for _, e := range entries {
		if e.IsDir() {
			add(filepath.Join(root, e.Name()))
		}
	}
	return out
}

// readProfile reads keys for the first of origins whose first key is in
// one profile's localStorage, from a private copy.
func readProfile(dir string, origins, keys []string) (map[string]string, string, error) {
	tmp, err := os.MkdirTemp("", "tokenops-localstorage-")
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := copyDir(dir, tmp); err != nil {
		return nil, "", err
	}
	db, err := leveldb.OpenFile(tmp, &opt.Options{ReadOnly: true, ErrorIfMissing: true})
	if err != nil {
		return nil, "", errors.New("the localStorage database could not be opened")
	}
	defer func() { _ = db.Close() }()
	for _, origin := range origins {
		values, err := readOrigin(db, origin, keys)
		if err != nil {
			return nil, "", err
		}
		for name := range values {
			if keyMatch(keys[0], name) {
				return values, origin, nil
			}
		}
	}
	return nil, "", ErrNotFound
}

// readOrigin reads keys of one origin. Chromium stores an entry under
// "_" + origin + "\x00" + an encoding byte + the key, and its value as an
// encoding byte and the string (0 is UTF-16LE, 1 is Latin-1).
func readOrigin(db *leveldb.DB, origin string, keys []string) (map[string]string, error) {
	prefix := []byte("_" + origin + "\x00")
	out := map[string]string{}
	it := db.NewIterator(util.BytesPrefix(prefix), nil)
	defer it.Release()
	for it.Next() {
		name, ok := decode(it.Key()[len(prefix):])
		if !ok || !wanted(keys, name) {
			continue
		}
		v, ok := decode(it.Value())
		if !ok {
			continue
		}
		out[name] = unquote(v)
	}
	if err := it.Error(); err != nil {
		return nil, errors.New("the localStorage database could not be read")
	}
	return out, nil
}

// wanted reports whether name is one of keys.
func wanted(keys []string, name string) bool {
	for _, k := range keys {
		if keyMatch(k, name) {
			return true
		}
	}
	return false
}

// keyMatch matches a key name against a pattern in which "*" matches any
// run of characters ("*auth1_session").
func keyMatch(pattern, name string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == name
	}
	if !strings.HasPrefix(name, parts[0]) {
		return false
	}
	name = name[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(name, p)
		if i < 0 {
			return false
		}
		name = name[i+len(p):]
	}
	return strings.HasSuffix(name, parts[len(parts)-1])
}

// decode reads Chromium's encoded string: a 0 byte and UTF-16LE, or a 1
// byte and Latin-1.
func decode(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	switch b[0] {
	case 1:
		r := make([]rune, 0, len(b)-1)
		for _, c := range b[1:] {
			r = append(r, rune(c))
		}
		return string(r), true
	case 0:
		body := b[1:]
		if len(body)%2 != 0 {
			return "", false
		}
		u := make([]uint16, len(body)/2)
		for i := range u {
			u[i] = uint16(body[2*i]) | uint16(body[2*i+1])<<8
		}
		return string(utf16.Decode(u)), true
	}
	return "", false
}

// unquote is a value stored as a JSON string, unquoted; anything else as
// stored.
func unquote(v string) string {
	t := strings.TrimSpace(v)
	if len(t) >= 2 && t[0] == '"' {
		var s string
		if json.Unmarshal([]byte(t), &s) == nil {
			return s
		}
	}
	return v
}

// copyDir copies a LevelDB directory's files, but not its LOCK, into to.
func copyDir(from, to string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	var total int64
	for _, e := range entries {
		if !e.Type().IsRegular() || e.Name() == "LOCK" {
			continue
		}
		n, err := copyFile(filepath.Join(from, e.Name()), filepath.Join(to, e.Name()), maxDB-total)
		if err != nil {
			return err
		}
		if total += n; total >= maxDB {
			return errors.New("the localStorage database is too large to copy")
		}
	}
	return nil
}

func copyFile(from, to string, limit int64) (int64, error) {
	src, err := os.Open(from) // #nosec G304 -- a file of the operator's own browser profile
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- in a directory this process just made
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(dst, io.LimitReader(src, limit))
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	return n, err
}
