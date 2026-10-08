// Package applogin reads another application's own sign-in on this
// machine: a CLI's token file, a desktop app's database, a Keychain item,
// a local server's token on its command line. It is the only code that
// does, and it is only ever called for a sign-in the operator granted with
// `tokenops vendor-usage setup <id> --use-app-login` (ADR 0013).
//
// What it promises:
//
//   - It reads exactly the item a descriptor names (providers.AppLoginItem), and
//     from it exactly the fields named; nothing else is parsed or returned.
//   - It never writes: a file is opened read-only, a database is read from
//     a private copy, and no token is ever refreshed or rotated, so the
//     owning application stays signed in.
//   - A Keychain item is only read quietly (keychain.Quiet): never a prompt,
//     and not at all with keychain.disabled.
//   - No error it returns carries a token or any of the item's content:
//     errors name the item and the field, nothing else.
package applogin

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	"go.klarlabs.de/tokenops/internal/infra/keychain"

	// Registers the sqlite driver the database sign-ins are read with.
	_ "modernc.org/sqlite"
)

// ErrNotFound is a sign-in that is not on this machine: no file, no field,
// no item, no process.
var ErrNotFound = errors.New("applogin: no sign-in found")

// maxItem bounds what is read from a file: sign-in files are small.
const maxItem = 1 << 20

// maxToken bounds one value read.
const maxToken = 64 << 10

// Env is where a sign-in is looked for. The zero value is this machine.
type Env struct {
	// Home is the home directory "~/" names; empty is the operator's.
	Home string
	// Getenv reads a path variable (AppLogin.PathEnv); nil is os.Getenv.
	Getenv func(string) string
	// Keychain reads an item quietly; nil is keychain.Quiet. It must
	// never prompt.
	Keychain func(keychain.Item) (string, error)
	// KeychainDisabled is keychain.disabled: no Keychain item is read.
	KeychainDisabled bool
	// Processes lists the command lines of this user's processes; nil
	// lists them from the system.
	Processes func(ctx context.Context) ([][]string, error)
}

func (e Env) home() (string, error) {
	if e.Home != "" {
		return e.Home, nil
	}
	return os.UserHomeDir()
}

func (e Env) getenv(k string) string {
	if e.Getenv != nil {
		return e.Getenv(k)
	}
	return os.Getenv(k)
}

// Located is one sign-in found on this machine: the item exactly as the
// operator is shown it and as a grant records it.
type Located struct {
	Spec providers.AppLoginItem
	// Item is the file or database's absolute path, the Keychain service,
	// or the process name.
	Item string
	// FromEnv is a path the spec's PathEnv named.
	FromEnv bool
}

// Describe says, in one line, exactly what is read: the item and its
// fields. It never reads the item.
func (l Located) Describe() string {
	fields := strings.Join(l.Spec.Fields, ", ")
	switch l.Spec.Kind {
	case providers.AppLoginKeychain:
		item := fmt.Sprintf("the Keychain item %q", l.Spec.Service)
		if l.Spec.Account != "" {
			item += fmt.Sprintf(" (account %q)", l.Spec.Account)
		}
		if len(l.Spec.Fields) == 1 && l.Spec.Fields[0] == "" {
			return item
		}
		return item + ", field " + fields
	case providers.AppLoginProcess:
		return fmt.Sprintf("the command line of the running %s process, flag %s", l.Spec.Process, fields)
	case providers.AppLoginSQLite:
		return fmt.Sprintf("the database %s, column %s (%s)", l.Item, fields, l.Spec.Query)
	case providers.AppLoginEnvFile:
		return fmt.Sprintf("the file %s, variable %s", l.Item, fields)
	case providers.AppLoginTextFile:
		return "the file " + l.Item
	}
	return fmt.Sprintf("the file %s, field %s", l.Item, fields)
}

// Locate finds which of specs is on this machine, in order, without reading
// any secret: a file's existence, a process's presence. A Keychain item is
// offered as is: whether it exists cannot be checked without reading it.
func Locate(ctx context.Context, specs []providers.AppLoginItem, env Env) (Located, error) {
	for _, s := range specs {
		if s.Kind == providers.AppLoginKeychain {
			if env.KeychainDisabled {
				continue
			}
			return Located{Spec: s, Item: s.Service}, nil
		}
		if s.Kind == providers.AppLoginProcess {
			if _, err := processArgs(ctx, s.Process, env); err == nil {
				return Located{Spec: s, Item: s.Process}, nil
			}
			continue
		}
		if p, fromEnv, ok := locateFile(s, env); ok {
			return Located{Spec: s, Item: p, FromEnv: fromEnv}, nil
		}
	}
	return Located{}, ErrNotFound
}

// locateFile is the first of spec's paths that is a regular file.
func locateFile(s providers.AppLoginItem, env Env) (string, bool, bool) {
	if s.PathEnv != "" {
		if p := strings.TrimSpace(env.getenv(s.PathEnv)); p != "" && filepath.IsAbs(p) && isFile(p) {
			return filepath.Clean(p), true, true
		}
	}
	for _, p := range expand(s, env) {
		if isFile(p) {
			return p, false, true
		}
	}
	return "", false, false
}

// expand is spec's paths with "~/" made the home directory.
func expand(s providers.AppLoginItem, env Env) []string {
	home, err := env.home()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(s.Paths))
	for _, p := range s.Paths {
		if rest, ok := strings.CutPrefix(p, "~/"); ok {
			out = append(out, filepath.Join(home, filepath.FromSlash(rest)))
		}
	}
	return out
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// Granted reports whether a grant recorded for item still names one of
// specs, with the same kind, fields and host. A grant is consent to read
// exactly what the operator was shown: when the descriptor changes what it
// would read or where it would send it, the grant no longer covers it.
func Granted(specs []providers.AppLoginItem, kind, item string, fields []string, host string, fromEnv bool, env Env) (Located, bool) {
	for _, s := range specs {
		if string(s.Kind) != kind || s.Host != host || !slices.Equal(s.Fields, fields) {
			continue
		}
		switch s.Kind {
		case providers.AppLoginKeychain:
			if s.Service == item {
				return Located{Spec: s, Item: item}, true
			}
		case providers.AppLoginProcess:
			if s.Process == item {
				return Located{Spec: s, Item: item}, true
			}
		default:
			if fromEnv && s.PathEnv != "" && filepath.IsAbs(item) {
				return Located{Spec: s, Item: filepath.Clean(item), FromEnv: true}, true
			}
			if slices.Contains(expand(s, env), item) {
				return Located{Spec: s, Item: item}, true
			}
		}
	}
	return Located{}, false
}

// Read reads the token from a located sign-in: the one field's value, or,
// for several, a JSON object of those present keyed by field. It is read
// afresh on every call, so a token the owning application refreshed is
// picked up.
func Read(ctx context.Context, l Located, env Env) (string, error) {
	values, err := read(ctx, l, env)
	if err != nil {
		return "", err
	}
	first := l.Spec.Fields[0]
	if v, ok := values[first]; !ok || v == "" {
		return "", fmt.Errorf("%w: %s has no %s", ErrNotFound, l.Item, fieldName(first))
	}
	for k, v := range values {
		if len(v) > maxToken {
			return "", fmt.Errorf("applogin: %s: %s is too long to be a sign-in", l.Item, fieldName(k))
		}
	}
	if len(l.Spec.Fields) == 1 {
		return values[first], nil
	}
	b, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func fieldName(f string) string {
	if f == "" {
		return "value"
	}
	return f
}

func read(ctx context.Context, l Located, env Env) (map[string]string, error) {
	s := l.Spec
	switch s.Kind {
	case providers.AppLoginKeychain:
		return readKeychain(s, env)
	case providers.AppLoginProcess:
		args, err := processArgs(ctx, s.Process, env)
		if err != nil {
			return nil, err
		}
		return flagValues(args, s.Fields), nil
	case providers.AppLoginSQLite:
		return readSQLite(ctx, l.Item, s)
	}
	raw, err := readFile(l.Item)
	if err != nil {
		return nil, err
	}
	switch s.Kind {
	case providers.AppLoginTextFile:
		first, _, _ := strings.Cut(string(raw), "\n")
		return map[string]string{s.Fields[0]: strings.TrimSpace(first)}, nil
	case providers.AppLoginEnvFile:
		return envValues(raw, s.Fields), nil
	}
	return jsonValues(raw, l.Item, s.Fields)
}

// readFile reads a sign-in file read-only, bounded.
func readFile(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- a path a descriptor names and the operator granted
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s is gone", ErrNotFound, path)
		}
		return nil, fmt.Errorf("applogin: open %s: %w", path, errors.Unwrap(err))
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("applogin: %s is not a regular file", path)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxItem+1))
	if err != nil {
		return nil, fmt.Errorf("applogin: read %s", path)
	}
	if len(raw) > maxItem {
		return nil, fmt.Errorf("applogin: %s is too large to be a sign-in file", path)
	}
	return raw, nil
}

// jsonValues reads fields out of a JSON document (see jsonPath). The parse
// error is not returned: it can quote the document.
func jsonValues(raw []byte, item string, fields []string) (map[string]string, error) {
	var doc any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("applogin: %s is not valid JSON", item)
	}
	out := map[string]string{}
	for _, f := range fields {
		for _, alt := range strings.Split(f, "|") {
			if v, ok := jsonPath(doc, alt); ok && v != "" {
				out[f] = v
				break
			}
		}
	}
	return out, nil
}

// pathSegments splits a field path at dots, except inside braces: a
// segment in braces is a key taken literally ("{https://auth.x.ai::*}"),
// in which "*" matches any run of characters.
func pathSegments(path string) []string {
	var (
		out   []string
		cur   strings.Builder
		depth int
	)
	for _, r := range path {
		switch {
		case r == '{':
			depth++
			cur.WriteRune(r)
		case r == '}' && depth > 0:
			depth--
			cur.WriteRune(r)
		case r == '.' && depth == 0:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(out, cur.String())
}

// child is the value under one path segment: a key, a braced key pattern
// (the first matching key in sorted order), or an array index.
func child(cur any, seg string) (any, bool) {
	switch v := cur.(type) {
	case map[string]any:
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			pattern := seg[1 : len(seg)-1]
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				if globMatch(pattern, k) {
					return v[k], true
				}
			}
			return nil, false
		}
		next, ok := v[seg]
		return next, ok
	case []any:
		i, err := strconv.Atoi(seg)
		if err != nil || i < 0 || i >= len(v) {
			return nil, false
		}
		return v[i], true
	}
	return nil, false
}

// globMatch reports whether s matches pattern, in which "*" matches any run
// of characters, "/" included.
func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// jsonPath is the value at a path: segments separated by dots, a braced
// segment being a literal key or key pattern. A string is returned as is, a
// number or bool as written, an object or array as JSON. An empty path is
// the whole document. (A field may list alternatives separated by "|";
// jsonValues reads the first present.)
func jsonPath(doc any, path string) (string, bool) {
	cur := doc
	if path != "" {
		for _, seg := range pathSegments(path) {
			next, ok := child(cur, seg)
			if !ok {
				return "", false
			}
			cur = next
		}
	}
	switch v := cur.(type) {
	case nil:
		return "", false
	case string:
		return strings.TrimSpace(v), true
	case json.Number:
		return v.String(), true
	case bool:
		return strconv.FormatBool(v), true
	}
	b, err := json.Marshal(cur)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// envValues reads variables out of a dotenv file: KEY=value lines, with an
// optional "export " and quotes.
func envValues(raw []byte, fields []string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), maxItem)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || !slices.Contains(fields, k) {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out
}

// readKeychain reads a Keychain item quietly; its value is the token, or
// JSON the fields are paths into.
func readKeychain(s providers.AppLoginItem, env Env) (map[string]string, error) {
	if env.KeychainDisabled {
		return nil, keychain.ErrDisabled
	}
	quiet := env.Keychain
	if quiet == nil {
		quiet = keychain.Quiet
	}
	v, err := quiet(keychain.Item{Service: s.Service, Account: s.Account})
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return nil, fmt.Errorf("%w: no Keychain item %q", ErrNotFound, s.Service)
		}
		return nil, err
	}
	if len(s.Fields) == 1 && s.Fields[0] == "" {
		return map[string]string{"": strings.TrimSpace(v)}, nil
	}
	return jsonValues([]byte(v), fmt.Sprintf("the Keychain item %q", s.Service), s.Fields)
}

// readSQLite runs a sign-in's SELECT on a private copy of the database, so
// the owning application's lock and files are never touched.
func readSQLite(ctx context.Context, path string, s providers.AppLoginItem) (map[string]string, error) {
	copyPath, cleanup, err := copyDB(path)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	db, err := sql.Open("sqlite", "file:"+copyPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("applogin: open %s", path)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return nil, fmt.Errorf("applogin: open %s", path)
	}
	row := db.QueryRowContext(ctx, s.Query)
	cols := make([]sql.NullString, len(s.Fields))
	ptrs := make([]any, len(cols))
	for i := range cols {
		ptrs[i] = &cols[i]
	}
	if err := row.Scan(ptrs...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s has no sign-in row", ErrNotFound, path)
		}
		return nil, fmt.Errorf("applogin: read %s: the sign-in query failed", path)
	}
	out := map[string]string{}
	for i, f := range s.Fields {
		if cols[i].Valid {
			out[f] = strings.TrimSpace(cols[i].String)
		}
	}
	return out, nil
}

// copyDB copies a database and its write-ahead sidecars to a private
// temporary directory, keeping the base name so SQLite pairs them.
func copyDB(path string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "tokenops-applogin-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	dst := filepath.Join(dir, filepath.Base(path))
	if err := copyFile(path, dst); err != nil {
		cleanup()
		return "", func() {}, err
	}
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
	src, err := os.Open(from) // #nosec G304 -- a database a descriptor names and the operator granted
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s is gone", ErrNotFound, from)
		}
		return fmt.Errorf("applogin: open %s", from)
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- in a directory this process just made
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, io.LimitReader(src, 256<<20)); err != nil {
		_ = dst.Close()
		return fmt.Errorf("applogin: copy %s", from)
	}
	return dst.Close()
}

// processArgs is the command line of the first of this user's processes
// whose executable's base name is name.
func processArgs(ctx context.Context, name string, env Env) ([]string, error) {
	list := env.Processes
	if list == nil {
		list = listProcesses
	}
	all, err := list(ctx)
	if err != nil {
		return nil, fmt.Errorf("applogin: list processes: %w", err)
	}
	for _, args := range all {
		if len(args) > 0 && filepath.Base(args[0]) == name {
			return args, nil
		}
	}
	return nil, fmt.Errorf("%w: no %s process is running", ErrNotFound, name)
}

// flagValues reads --flag=value and --flag value out of a command line.
func flagValues(args []string, flags []string) map[string]string {
	out := map[string]string{}
	for i, a := range args {
		for _, f := range flags {
			if v, ok := strings.CutPrefix(a, f+"="); ok {
				out[f] = v
			} else if a == f && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				out[f] = args[i+1]
			}
		}
	}
	return out
}
