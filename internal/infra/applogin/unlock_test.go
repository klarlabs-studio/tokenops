package applogin

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
	"go.klarlabs.de/tokenops/internal/infra/keychain"
)

const unlockQuery = "SELECT value FROM ItemTable WHERE key = 'cursorAuth/accessToken' LIMIT 1"

// sealedSpec is a database sign-in whose value may be sealed under a
// Keychain item, as Cursor's state database is.
func sealedSpec() providers.AppLoginItem {
	return providers.AppLoginItem{
		App: "Cursor", Kind: providers.AppLoginSQLite,
		Paths: []string{"~/state.vscdb"}, Query: unlockQuery, Fields: []string{"value"}, Host: "cursor.com",
		Unlock: &providers.AppLoginItem{Kind: providers.AppLoginKeychain, Service: "Cursor Safe Storage", Account: "Cursor Key", Fields: []string{""}},
		Sealed: sealedPrefix,
	}
}

// stateDB writes a state database holding value under the key the query reads.
func stateDB(t *testing.T, home, value string) string {
	t.Helper()
	path := filepath.Join(home, "state.vscdb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, q := range []string{
		`CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`,
		`INSERT INTO ItemTable VALUES ('cursorAuth/accessToken', '` + value + `')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func unlockEnv(t *testing.T, home string, calls *[]keychain.Item) Env {
	t.Helper()
	return Env{Home: home, Getenv: func(string) string { return "" },
		Keychain: func(i keychain.Item) (string, error) {
			*calls = append(*calls, i)
			if i.Service != "Cursor Safe Storage" || i.Account != "Cursor Key" {
				return "", keychain.ErrNotFound
			}
			return sealedPassword, nil
		}}
}

// A sealed value is opened with the password of the Keychain item the grant
// names, and the plaintext is what the reader gets.
func TestReadOpensSealedValueWithItsKeychainItem(t *testing.T) {
	home := t.TempDir()
	stateDB(t, home, sealedVector)
	var calls []keychain.Item
	env := unlockEnv(t, home, &calls)
	l, err := Locate(context.Background(), []providers.AppLoginItem{sealedSpec()}, env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(context.Background(), l, env)
	if err != nil || got != sealedPlain {
		t.Fatalf("Read() = %q, %v", got, err)
	}
	if len(calls) != 1 || calls[0] != (keychain.Item{Service: "Cursor Safe Storage", Account: "Cursor Key"}) {
		t.Errorf("Keychain reads %v, want the one item", calls)
	}
}

// A value that is not sealed is read as it is, and the Keychain is not touched.
func TestReadPlainValueSkipsTheKeychain(t *testing.T) {
	home := t.TempDir()
	stateDB(t, home, "plain-token-value")
	var calls []keychain.Item
	env := unlockEnv(t, home, &calls)
	l, err := Locate(context.Background(), []providers.AppLoginItem{sealedSpec()}, env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(context.Background(), l, env)
	if err != nil || got != "plain-token-value" || len(calls) != 0 {
		t.Fatalf("Read() = %q, %v; Keychain reads %v", got, err, calls)
	}
}

// With no Keychain item, or with keychain.disabled, a sealed value is not
// read, and the error carries neither the value nor the password.
func TestReadSealedRefusedWithoutItsKeychainItem(t *testing.T) {
	home := t.TempDir()
	stateDB(t, home, sealedVector)
	var calls []keychain.Item
	missing := unlockEnv(t, home, &calls)
	missing.Keychain = func(keychain.Item) (string, error) { return "", keychain.ErrNotFound }
	l, err := Locate(context.Background(), []providers.AppLoginItem{sealedSpec()}, missing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), l, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing item: %v", err)
	}

	disabled := unlockEnv(t, home, &calls)
	disabled.KeychainDisabled = true
	if _, err := Read(context.Background(), l, disabled); err == nil {
		t.Error("keychain.disabled still opened the sealed value")
	}
	if len(calls) != 0 {
		t.Errorf("the Keychain was read: %v", calls)
	}
}

// Setup reads the item with the operator's prompt, once, and the daemon
// never does: the quiet read is not used when a prompt is given.
func TestReadSealedUsesThePromptWhenSetupGivesOne(t *testing.T) {
	home := t.TempDir()
	stateDB(t, home, sealedVector)
	var quietCalls, promptCalls []keychain.Item
	env := unlockEnv(t, home, &quietCalls)
	env.Prompt = func(_ context.Context, i keychain.Item) (string, error) {
		promptCalls = append(promptCalls, i)
		return sealedPassword, nil
	}
	l, err := Locate(context.Background(), []providers.AppLoginItem{sealedSpec()}, env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(context.Background(), l, env)
	if err != nil || got != sealedPlain {
		t.Fatalf("Read() = %q, %v", got, err)
	}
	if len(promptCalls) != 1 || len(quietCalls) != 0 {
		t.Errorf("prompt reads %v, quiet reads %v", promptCalls, quietCalls)
	}
}

// The grant covers the Keychain item as well: changing it, or granting
// without it, does not cover the sign-in.
func TestGrantCoversTheUnlockItem(t *testing.T) {
	home := t.TempDir()
	path := stateDB(t, home, sealedVector)
	env := Env{Home: home, Getenv: func(string) string { return "" }}
	specs := []providers.AppLoginItem{sealedSpec()}
	cover := keychain.Item{Service: "Cursor Safe Storage", Account: "Cursor Key"}
	if _, ok := Granted(specs, "sqlite", path, []string{"value"}, "cursor.com", false, cover, env); !ok {
		t.Fatal("the grant the descriptor shows is not covered by it")
	}
	for name, unlock := range map[string]keychain.Item{
		"another account": {Service: "Cursor Safe Storage", Account: "Cursor"},
		"another service": {Service: "Other", Account: "Cursor Key"},
		"no unlock item":  {},
	} {
		if _, ok := Granted(specs, "sqlite", path, []string{"value"}, "cursor.com", false, unlock, env); ok {
			t.Errorf("covered with %s", name)
		}
	}
	plain := providers.AppLoginItem{App: "X", Kind: providers.AppLoginSQLite, Paths: []string{"~/state.vscdb"},
		Query: unlockQuery, Fields: []string{"value"}, Host: "cursor.com"}
	if _, ok := Granted([]providers.AppLoginItem{plain}, "sqlite", path, []string{"value"}, "cursor.com", false, cover, env); ok {
		t.Error("a grant names a Keychain item the descriptor does not read")
	}
	if _, ok := Granted([]providers.AppLoginItem{plain}, "sqlite", path, []string{"value"}, "cursor.com", false, keychain.Item{}, env); !ok {
		t.Error("an old grant without an unlock item no longer covers a plain sign-in")
	}
}
