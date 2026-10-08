package browserstorage

import (
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

// latin1 and utf16le encode a string as Chromium stores it.
func latin1(s string) []byte { return append([]byte{1}, []byte(s)...) }

func utf16le(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 1, 1+2*len(units))
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func entry(origin string, key []byte) []byte {
	return append([]byte("_"+origin+"\x00"), key...)
}

// profile writes a fixture localStorage LevelDB into a temporary home, as
// Chrome lays it out, and returns the open database: like a running
// browser, it holds the lock while the test reads.
func profile(t *testing.T, home, browserDir, name string, entries map[string][]byte) *leveldb.DB {
	t.Helper()
	dir := filepath.Join(home, browserDir, name, "Local Storage", "leveldb")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Put([]byte("VERSION"), []byte("1"), nil); err != nil {
		t.Fatal(err)
	}
	for k, v := range entries {
		if err := db.Put([]byte(k), v, nil); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

const chrome = "Library/Application Support/Google/Chrome"

func TestFindReadsOnlyTheNamedKeysOfOneOrigin(t *testing.T) {
	home := t.TempDir()
	devin, windsurf := "https://app.devin.ai", "https://windsurf.com"
	db := profile(t, home, chrome, "Default", map[string][]byte{
		string(entry(devin, latin1("devin_session_token"))):  latin1(`"devin-session-abc"`),
		string(entry(devin, utf16le("devin_account_id"))):    utf16le(`"acct-ü"`),
		string(entry(devin, latin1("unrelated_secret"))):     latin1("must-not-be-read"),
		string(entry(windsurf, latin1("devin_auth1_token"))): latin1(`"other-origin"`),
		"META:" + devin: []byte("meta"),
	})
	before := listing(t, filepath.Join(home, chrome, "Default", "Local Storage", "leveldb"))
	f, err := Find(home, []string{devin, windsurf}, []string{"devin_session_token", "devin_auth1_token", "devin_account_id"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.Browser != "Chrome" || f.Origin != devin || len(f.Values) != 2 ||
		f.Values["devin_session_token"] != "devin-session-abc" || f.Values["devin_account_id"] != "acct-ü" {
		t.Errorf("%+v", f)
	}
	if strings.Contains(f.JSON(), "must-not-be-read") || strings.Contains(f.JSON(), "other-origin") {
		t.Errorf("read more than asked: %s", f.JSON())
	}
	if after := listing(t, filepath.Join(home, chrome, "Default", "Local Storage", "leveldb")); after != before {
		t.Errorf("the browser's files changed:\n%s\n%s", before, after)
	}
	_ = db
}

// The legacy origin is read when the first has no session; origins are
// never mixed.
func TestFindFallsBackToTheNextOrigin(t *testing.T) {
	home := t.TempDir()
	profile(t, home, chrome, "Profile 2", map[string][]byte{
		string(entry("https://windsurf.com", latin1("devin_session_token"))): latin1("legacy"),
		string(entry("https://app.devin.ai", latin1("devin_account_id"))):    latin1("acct"),
	})
	f, err := Find(home, []string{"https://app.devin.ai", "https://windsurf.com"}, []string{"devin_session_token", "devin_account_id"}, "")
	if err != nil || f.Origin != "https://windsurf.com" || f.Values["devin_session_token"] != "legacy" || f.Values["devin_account_id"] != "" {
		t.Errorf("%+v %v", f, err)
	}
}

// Entries compacted into table files (.ldb, snappy-compressed), as a
// closed browser leaves them, are read too.
func TestFindReadsCompactedTables(t *testing.T) {
	home := t.TempDir()
	db := profile(t, home, chrome, "Default", map[string][]byte{
		string(entry("https://app.devin.ai", latin1("devin_session_token"))): latin1(strings.Repeat("s", 5000)),
	})
	if err := db.CompactRange(util.Range{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := Find(home, []string{"https://app.devin.ai"}, []string{"devin_session_token"}, "Chrome")
	if err != nil || len(f.Values["devin_session_token"]) != 5000 {
		t.Errorf("%v %d", err, len(f.Values["devin_session_token"]))
	}
}

func TestFindNotFound(t *testing.T) {
	home := t.TempDir()
	profile(t, home, chrome, "Default", map[string][]byte{string(entry("https://other.example", latin1("devin_session_token"))): latin1("x")})
	if _, err := Find(home, []string{"https://app.devin.ai"}, []string{"devin_session_token"}, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := Find(home, []string{"https://app.devin.ai"}, []string{"devin_session_token"}, "Brave"); !errors.Is(err, ErrNotFound) {
		t.Errorf("only Brave: %v", err)
	}
	if _, err := Find(t.TempDir(), []string{"https://app.devin.ai"}, []string{"devin_session_token"}, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("no browser: %v", err)
	}
}

func TestDecode(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want string
		ok   bool
	}{
		{latin1("abc"), "abc", true},
		{utf16le("Grüße"), "Grüße", true},
		{[]byte{0, 1}, "", false},
		{[]byte{7, 'a'}, "", false},
		{nil, "", false},
	} {
		if got, ok := decode(tc.in); got != tc.want || ok != tc.ok {
			t.Errorf("%v: %q %v", tc.in, got, ok)
		}
	}
	if unquote(`"a\"b"`) != `a"b` || unquote(`{"x":1}`) != `{"x":1}` || unquote("plain") != "plain" {
		t.Error("unquote")
	}
}

// Only setup reads localStorage: the one package that imports this one is
// the capability behind `tokenops vendor-usage setup`, which the daemon
// and the MCP server do not call to read a browser.
func TestOnlySetupImportsBrowserStorage(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	const self = "go.klarlabs.de/tokenops/internal/infra/browserstorage"
	allowed := map[string]bool{
		filepath.Join(root, "internal/capability/providersetup"): true,
		filepath.Join(root, "internal/infra/browserstorage"):     true,
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "worktrees") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return nil //nolint:nilerr // generated or broken files are not this test's concern
		}
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == self && !allowed[filepath.Dir(path)] {
				t.Errorf("%s reads browser localStorage; only setup may (ADR 0013)", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// listing is a directory's file names and sizes.
func listing(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		info, _ := e.Info()
		b.WriteString(e.Name())
		if info != nil {
			b.WriteString(":" + info.ModTime().String())
		}
		b.WriteString(" ")
	}
	return b.String()
}
