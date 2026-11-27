package applogin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	"go.klarlabs.de/tokenops/internal/infra/keychain"
	"go.klarlabs.de/tokenops/internal/sqliteuri"
)

const secret = "tok-SECRET-123"

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// noKeychain fails the test if anything reads the Keychain.
func noKeychain(t *testing.T) func(keychain.Item) (string, error) {
	return func(i keychain.Item) (string, error) {
		t.Errorf("read the Keychain item %s", i)
		return "", keychain.ErrNotFound
	}
}

func TestReadsEachKind(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".app/auth.json"), `{"kilo":{"access":"`+secret+`","refresh":"r"},"n":7}`)
	write(t, filepath.Join(home, ".app/.env"), "# c\nexport OTHER=x\nAPP_KEY=\""+secret+"\"\n")
	write(t, filepath.Join(home, ".app/token"), secret+"\n")
	env := Env{Home: home, Keychain: noKeychain(t)}
	for _, tc := range []struct {
		spec providers.AppLoginItem
		want string
	}{
		{providers.AppLoginItem{Kind: providers.AppLoginJSON, Paths: []string{"~/.missing.json", "~/.app/auth.json"}, Fields: []string{"kilo.access"}}, secret},
		{providers.AppLoginItem{Kind: providers.AppLoginJSON, Paths: []string{"~/.app/auth.json"}, Fields: []string{"kilo.access", "n", "absent"}}, `{"kilo.access":"` + secret + `","n":"7"}`},
		{providers.AppLoginItem{Kind: providers.AppLoginEnvFile, Paths: []string{"~/.app/.env"}, Fields: []string{"APP_KEY"}}, secret},
		{providers.AppLoginItem{Kind: providers.AppLoginTextFile, Paths: []string{"~/.app/token"}, Fields: []string{"token"}}, secret},
	} {
		l, err := Locate(context.Background(), []providers.AppLoginItem{tc.spec}, env)
		if err != nil {
			t.Fatalf("%s: %v", tc.spec.Kind, err)
		}
		got, err := Read(context.Background(), l, env)
		if err != nil || got != tc.want {
			t.Errorf("%s: %q, %v; want %q", tc.spec.Kind, got, err, tc.want)
		}
		if strings.Contains(l.Describe(), secret) {
			t.Errorf("Describe shows the token")
		}
	}
}

func TestPathEnvOverrides(t *testing.T) {
	home := t.TempDir()
	alt := filepath.Join(t.TempDir(), "tok")
	write(t, alt, secret)
	spec := providers.AppLoginItem{Kind: providers.AppLoginTextFile, Paths: []string{"~/.x"}, PathEnv: "X_PATH", Fields: []string{"token"}, Host: "h"}
	env := Env{Home: home, Getenv: func(k string) string {
		if k == "X_PATH" {
			return alt
		}
		return ""
	}}
	l, err := Locate(context.Background(), []providers.AppLoginItem{spec}, env)
	if err != nil || l.Item != alt || !l.FromEnv {
		t.Fatalf("%+v %v", l, err)
	}
	if _, ok := Granted([]providers.AppLoginItem{spec}, string(spec.Kind), alt, spec.Fields, "h", true, keychain.Item{}, Env{Home: home}); !ok {
		t.Error("a grant of the variable's path is honoured")
	}
	if _, ok := Granted([]providers.AppLoginItem{spec}, string(spec.Kind), alt, spec.Fields, "h", false, keychain.Item{}, Env{Home: home}); ok {
		t.Error("a path outside the descriptor's is not read without having come from its variable")
	}
}

func TestSQLiteIsReadFromACopy(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "Library/kiro/data.sqlite3")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE auth_kv(key TEXT, value TEXT); INSERT INTO auth_kv VALUES('kirocli:social:token', '{"access_token":"` + secret + `"}')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	before, _ := os.ReadFile(path)
	spec := providers.AppLoginItem{Kind: providers.AppLoginSQLite, Paths: []string{"~/Library/kiro/data.sqlite3"},
		Query: "SELECT value FROM auth_kv WHERE key = 'kirocli:social:token'", Fields: []string{"value"}}
	env := Env{Home: home}
	l, err := Locate(context.Background(), []providers.AppLoginItem{spec}, env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(context.Background(), l, env)
	if err != nil || !strings.Contains(got, secret) {
		t.Fatalf("%q %v", got, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("the database was modified")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("files appeared beside the database: %v", entries)
	}
}

func TestKeychainOnlyQuietlyAndNeverWhenDisabled(t *testing.T) {
	spec := providers.AppLoginItem{Kind: providers.AppLoginKeychain, Service: "ai.meta.dev.credentials", Fields: []string{"accessToken"}}
	var asked []keychain.Item
	env := Env{Home: t.TempDir(), Keychain: func(i keychain.Item) (string, error) {
		asked = append(asked, i)
		return `{"accessToken":"` + secret + `"}`, nil
	}}
	l, err := Locate(context.Background(), []providers.AppLoginItem{spec}, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 0 {
		t.Fatal("locating read the Keychain")
	}
	if got, err := Read(context.Background(), l, env); err != nil || got != secret {
		t.Fatalf("%q %v", got, err)
	}
	disabled := env
	disabled.KeychainDisabled = true
	asked = nil
	if _, err := Locate(context.Background(), []providers.AppLoginItem{spec}, disabled); !errors.Is(err, ErrNotFound) {
		t.Errorf("keychain.disabled offers no Keychain item: %v", err)
	}
	if _, err := Read(context.Background(), l, disabled); !errors.Is(err, keychain.ErrDisabled) || len(asked) != 0 {
		t.Errorf("keychain.disabled read it: %v, %d reads", err, len(asked))
	}
	// A quiet read that would prompt is reported, not retried with a prompt.
	env.Keychain = func(keychain.Item) (string, error) { return "", keychain.ErrInteractionRequired }
	if _, err := Read(context.Background(), l, env); !errors.Is(err, keychain.ErrInteractionRequired) {
		t.Errorf("err %v", err)
	}
}

func TestProcessFlags(t *testing.T) {
	spec := providers.AppLoginItem{Kind: providers.AppLoginProcess, Process: "language_server*|language-server*",
		Markers: []string{"antigravity"}, Fields: []string{"--csrf_token", "--extension_server_port", "pid"}}
	env := Env{Processes: func(context.Context) ([]Process, error) {
		return []Process{
			{PID: 1, Args: []string{"/usr/bin/zsh"}},
			{PID: 7, Args: []string{"/Applications/Cursor.app/x/language_server_macos_arm", "--csrf_token", "other"}},
			{PID: 42, Args: []string{"/Applications/Antigravity.app/x/language_server_macos_arm", "--csrf_token", secret, "--extension_server_port=4242"}},
		}, nil
	}}
	l, err := Locate(context.Background(), []providers.AppLoginItem{spec}, env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(context.Background(), l, env)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if json.Unmarshal([]byte(got), &m) != nil || m["--csrf_token"] != secret || m["--extension_server_port"] != "4242" || m["pid"] != "42" {
		t.Errorf("%q", got)
	}
	none := Env{Processes: func(context.Context) ([]Process, error) { return nil, nil }}
	if _, err := Locate(context.Background(), []providers.AppLoginItem{spec}, none); !errors.Is(err, ErrNotFound) {
		t.Errorf("no process: %v", err)
	}
}

// No error carries the token or the file's content.
func TestErrorsNeverCarryContent(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "bad.json"), `{"token": "`+secret+`" ,,}`)
	write(t, filepath.Join(home, "empty.json"), `{"other":"`+secret+`"}`)
	env := Env{Home: home}
	for _, p := range []string{"~/bad.json", "~/empty.json"} {
		spec := providers.AppLoginItem{Kind: providers.AppLoginJSON, Paths: []string{p}, Fields: []string{"token"}}
		l, err := Locate(context.Background(), []providers.AppLoginItem{spec}, env)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Read(context.Background(), l, env)
		if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "token\": ") {
			t.Errorf("%s: %v", p, err)
		}
	}
}

// A grant covers exactly what it recorded: another kind, field, host or
// path is not read.
func TestGrantedMatchesExactly(t *testing.T) {
	home := t.TempDir()
	spec := providers.AppLoginItem{Kind: providers.AppLoginJSON, Paths: []string{"~/.a.json"}, Fields: []string{"t"}, Host: "api.x"}
	specs := []providers.AppLoginItem{spec}
	env := Env{Home: home}
	item := filepath.Join(home, ".a.json")
	if _, ok := Granted(specs, "json-file", item, []string{"t"}, "api.x", false, keychain.Item{}, env); !ok {
		t.Fatal("the recorded grant is honoured")
	}
	for _, bad := range []struct {
		kind, item, field, host string
	}{
		{"env-file", item, "t", "api.x"},
		{"json-file", "/etc/passwd", "t", "api.x"},
		{"json-file", item, "refresh", "api.x"},
		{"json-file", item, "t", "evil.example"},
	} {
		if _, ok := Granted(specs, bad.kind, bad.item, []string{bad.field}, bad.host, false, keychain.Item{}, env); ok {
			t.Errorf("%+v was honoured", bad)
		}
	}
}

// Keys with dots are matched in braces, "*" matches any run of characters,
// and "|" falls back to the next path.
func TestFieldPatternsAndAlternatives(t *testing.T) {
	doc := []byte(`{"https://accounts.x.ai/sign-in":{"key":"old"},"https://auth.x.ai::client-1":{"key":"` + secret + `"},"authToken":"top"}`)
	for field, want := range map[string]string{
		"{https://auth.x.ai::*}.key|{*/sign-in*}.key": secret,
		"{https://nope::*}.key|{*/sign-in*}.key":      "old",
		"default.authToken|authToken":                 "top",
	} {
		got, err := jsonValues(doc, "f", []string{field})
		if err != nil || got[field] != want {
			t.Errorf("%s: %q %v; want %q", field, got[field], err, want)
		}
	}
}

// Cursor's state.vscdb also holds its chats and runs to gigabytes; a
// sign-in in a database larger than any copy limit is still read, in
// place and read-only.
func TestSQLiteLargerThanTheCopyLimitIsRead(t *testing.T) {
	old := copyLimit
	copyLimit = 4 << 10
	t.Cleanup(func() { copyLimit = old })
	home := t.TempDir()
	path := filepath.Join(home, "Library/cursor/state.vscdb")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE ItemTable(key TEXT UNIQUE, value BLOB); CREATE TABLE cursorDiskKV(key TEXT, value BLOB)`); err != nil {
		t.Fatal(err)
	}
	pad := strings.Repeat("x", 1<<10)
	for i := range 64 {
		if _, err := db.Exec(`INSERT INTO cursorDiskKV VALUES(?, ?)`, fmt.Sprint("bubbleId:", i), pad); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO ItemTable VALUES('cursorAuth/accessToken', ?)`, secret); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if fi, _ := os.Stat(path); fi.Size() <= copyLimit {
		t.Fatalf("fixture is %d bytes, not over the %d copy limit", fi.Size(), copyLimit)
	}
	got := readCursorFixture(t, home)
	if !strings.Contains(got, secret) {
		t.Fatalf("got %q", got)
	}
}

// A running Cursor keeps recent writes in its write-ahead log; the sign-in
// written there, not yet checkpointed, is what is read.
func TestSQLiteSignInStillInTheWriteAheadLogIsRead(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "Library/cursor/state.vscdb")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	app, err := sql.Open("sqlite", sqliteuri.File(path, "_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)"))
	if err != nil {
		t.Fatal(err)
	}
	app.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = app.Close() })
	if _, err := app.Exec(`CREATE TABLE ItemTable(key TEXT UNIQUE, value BLOB); INSERT INTO ItemTable VALUES('cursorAuth/accessToken', ?)`, secret); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path + "-wal"); err != nil || fi.Size() == 0 {
		t.Fatalf("fixture's write-ahead log is empty: %v", err)
	}
	got := readCursorFixture(t, home)
	if !strings.Contains(got, secret) {
		t.Fatalf("got %q", got)
	}
	var n int
	if err := app.QueryRow(`SELECT count(*) FROM ItemTable`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the application's own connection was disturbed: %d %v", n, err)
	}
}

// A copy never silently stops at its limit: a database larger than it is
// refused, not read as a truncated file SQLite calls malformed.
func TestCopyRefusesADatabaseLargerThanItsLimit(t *testing.T) {
	old := copyLimit
	copyLimit = 16
	t.Cleanup(func() { copyLimit = old })
	path := filepath.Join(t.TempDir(), "big.db")
	if err := os.WriteFile(path, make([]byte, 64), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, cleanup, err := copyDB(path); err == nil {
		cleanup()
		t.Fatal("a database over the copy limit was copied")
	}
}

func readCursorFixture(t *testing.T, home string) string {
	t.Helper()
	spec := providers.AppLoginItem{Kind: providers.AppLoginSQLite, Paths: []string{"~/Library/cursor/state.vscdb"},
		Query: "SELECT value FROM ItemTable WHERE key = 'cursorAuth/accessToken' LIMIT 1", Fields: []string{"value"}}
	env := Env{Home: home}
	l, err := Locate(context.Background(), []providers.AppLoginItem{spec}, env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(context.Background(), l, env)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return got
}
