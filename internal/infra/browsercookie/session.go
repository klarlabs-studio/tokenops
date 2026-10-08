package browsercookie

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/infra/keychain"
)

// Session describes the cookies one web session is made of: the cookies a
// browser sends to Hosts, as a vendor's own web page would send them.
type Session struct {
	// Hosts are the hosts the session's requests go to, tried in order (a
	// vendor's international console, then its mainland one). The session
	// is the cookies of the first host that holds one, all from one store.
	Hosts []string
	// Names are the cookies read. A name ending in "*" is a prefix
	// ("ory_session_*"). With AllForHost, every cookie the browser would
	// send to the host is read instead, and Names are not consulted.
	Names []string
	// Proof are the cookies (a "*" suffix is a prefix) that show someone is
	// signed in: a store holding none of them has no session, and nothing
	// in it is decrypted. Empty: the first of Names, or with AllForHost and
	// no Names, any cookie at all.
	Proof []string
	// AllForHost reads every cookie the browser would send to the host:
	// for a vendor whose session cookie names are not known. It never
	// reads a cookie of another site.
	AllForHost bool
}

// Cookie is one cookie read for a session.
type Cookie struct {
	Name, Value string
}

// Found is a session read from one browser store.
type Found struct {
	Cookies []Cookie
	// Host is the entry of Session.Hosts the cookies were found for.
	Host    string
	Browser Browser
}

// Header renders the cookies as one Cookie header value.
func (f Found) Header() string {
	parts := make([]string, 0, len(f.Cookies))
	for _, c := range f.Cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// Map is the cookies by name.
func (f Found) Map() map[string]string {
	out := make(map[string]string, len(f.Cookies))
	for _, c := range f.Cookies {
		out[c.Name] = c.Value
	}
	return out
}

// FindSession reads s from the first browser store that holds it: only,
// when set, limits the search to that browser. A store is asked for the
// Keychain secret only once it is known to hold a proof cookie, and each
// browser's secret is read at most once per call, so macOS asks at most
// once for the one browser that has the session. A refused prompt stops
// the search through other Chromium browsers (a prompt storm is not a
// fallback); Firefox, which needs no Keychain, is still read.
func FindSession(ctx context.Context, home string, s Session, only string, secret SecretFunc) (Found, error) {
	if len(s.Hosts) == 0 {
		return Found{}, errors.New("browsercookie: no host given")
	}
	if len(s.Names) == 0 && !s.AllForHost {
		return Found{}, errors.New("browsercookie: no cookie names given")
	}
	if secret == nil {
		secret = QuietSecret()
	}
	var firstErr error
	tried, denied := 0, false
	for _, b := range known {
		if only != "" && !strings.EqualFold(only, b.Name) {
			continue
		}
		root := filepath.Join(home, b.dir)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		tried++
		if denied && !b.firefox {
			continue
		}
		stores, err := b.stores(root)
		if err != nil {
			continue
		}
		key := memoSecret(b, secret)
		for _, store := range stores {
			found, err := b.readSession(ctx, store, s, key)
			if err == nil {
				return found, nil
			}
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", b.Name, err)
			}
			if errors.Is(err, keychain.ErrDenied) {
				denied = true
				break
			}
		}
	}
	if firstErr != nil {
		return Found{}, firstErr
	}
	if tried == 0 {
		return Found{}, fmt.Errorf("browsercookie: no supported browser found (looked for %s)", strings.Join(Names(), ", "))
	}
	return Found{}, ErrNotFound
}

// memoSecret asks for b's secret at most once.
func memoSecret(b Browser, secret SecretFunc) func() (string, error) {
	var (
		asked bool
		v     string
		err   error
	)
	return func() (string, error) {
		if !asked {
			asked = true
			v, err = secret(b)
		}
		return v, err
	}
}

// row is one stored cookie.
type row struct {
	host, name, value string
	enc               []byte
}

// readSession reads s from one store, trying each host in turn.
func (b Browser) readSession(ctx context.Context, store string, s Session, secret func() (string, error)) (Found, error) {
	tmp, cleanup, err := copyStore(store)
	if err != nil {
		return Found{}, err
	}
	defer cleanup()
	db, err := sql.Open("sqlite", "file:"+tmp+"?mode=ro")
	if err != nil {
		return Found{}, fmt.Errorf("browsercookie: open store: %w", err)
	}
	defer func() { _ = db.Close() }()
	for _, host := range s.Hosts {
		rows, err := b.hostRows(ctx, db, host)
		if err != nil {
			return Found{}, err
		}
		picked := pick(rows, s)
		if len(picked) == 0 || !proven(picked, s) {
			continue
		}
		out := Found{Host: host, Browser: b}
		for _, r := range picked {
			v := r.value
			if v == "" && len(r.enc) > 0 {
				if b.firefox {
					continue
				}
				key, err := secret()
				if err != nil {
					// Naming the keychain matters: a refused prompt is the
					// operator's to retry, unlike a cookie that is not there.
					return Found{}, fmt.Errorf("browsercookie: keychain secret unavailable: %w", err)
				}
				if v, err = decryptChromium(r.enc, deriveChromiumKey(key)); err != nil {
					continue
				}
			}
			if v != "" {
				out.Cookies = append(out.Cookies, Cookie{Name: r.name, Value: v})
			}
		}
		if len(out.Cookies) > 0 && proven(toRows(out.Cookies), s) {
			return out, nil
		}
	}
	return Found{}, ErrNotFound
}

func toRows(cs []Cookie) []row {
	out := make([]row, 0, len(cs))
	for _, c := range cs {
		out = append(out, row{name: c.Name})
	}
	return out
}

// hostRows returns every cookie stored for one of host's variants, most
// specific host first.
func (b Browser) hostRows(ctx context.Context, db *sql.DB, host string) ([]row, error) {
	variants := hostVariants(host)
	q := `SELECT host_key, name, value, encrypted_value FROM cookies WHERE host_key IN (` + placeholders(len(variants)) + `)`
	if b.firefox {
		q = `SELECT host, name, value, NULL FROM moz_cookies WHERE host IN (` + placeholders(len(variants)) + `)`
	}
	args := make([]any, len(variants))
	for i, v := range variants {
		args[i] = v
	}
	rs, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("browsercookie: read store: %w", err)
	}
	defer func() { _ = rs.Close() }()
	rank := map[string]int{}
	for i, v := range variants {
		rank[v] = i
	}
	var out []row
	for rs.Next() {
		var r row
		var v sql.NullString
		if err := rs.Scan(&r.host, &r.name, &v, &r.enc); err != nil {
			return nil, fmt.Errorf("browsercookie: read store: %w", err)
		}
		r.value = v.String
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("browsercookie: read store: %w", err)
	}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].host] < rank[out[j].host] })
	return out, nil
}

// pick keeps the cookies s reads, one per name, the most specific host's
// first, in the order of s.Names (prefix matches and AllForHost by name).
func pick(rows []row, s Session) []row {
	seen := map[string]bool{}
	var out []row
	add := func(r row) {
		if !seen[r.name] && r.name != "" {
			seen[r.name] = true
			out = append(out, r)
		}
	}
	if s.AllForHost {
		sorted := append([]row(nil), rows...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
		for _, r := range sorted {
			add(r)
		}
		return out
	}
	for _, n := range s.Names {
		var matches []row
		for _, r := range rows {
			if nameMatches(n, r.name) {
				matches = append(matches, r)
			}
		}
		sort.SliceStable(matches, func(i, j int) bool { return matches[i].name < matches[j].name })
		for _, r := range matches {
			add(r)
		}
	}
	return out
}

// proven reports whether rows hold one of s's proof cookies.
func proven(rows []row, s Session) bool {
	proof := s.Proof
	if len(proof) == 0 {
		if len(s.Names) == 0 {
			return len(rows) > 0
		}
		proof = s.Names[:1]
	}
	for _, p := range proof {
		for _, r := range rows {
			if nameMatches(p, r.name) {
				return true
			}
		}
	}
	return false
}

// nameMatches matches a cookie name against a name or a "prefix*".
func nameMatches(pattern, name string) bool {
	if p, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(name, p) && name != p
	}
	return pattern == name
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
