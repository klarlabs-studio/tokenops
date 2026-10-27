package harnesskeys

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"go.klarlabs.de/tokenops/internal/infra/codexsettings"
)

// Invented values only. The rows mirror opencode 2's credential table.
type opencodeRow struct {
	integration, value string
	active             int
}

// writeOpencodeDB creates opencode.db in dir with the given credential rows.
func writeOpencodeDB(t *testing.T, path string, rows []opencodeRow) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE credential (integration_id TEXT NOT NULL, value TEXT NOT NULL, active INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO credential (integration_id, value, active) VALUES (?, ?, ?)`, r.integration, r.value, r.active); err != nil {
			t.Fatal(err)
		}
	}
}

// findOpencode runs Find with only opencode's sources: no environment, no
// Claude or Codex settings, so nothing of the machine's is read.
func findOpencode(t *testing.T, data string, env map[string]string) []Credential {
	t.Helper()
	return Find(Options{
		Getenv:         func(k string) string { return env[k] },
		OpencodeData:   data,
		OpencodeConfig: t.TempDir(),
		Claude:         func() (string, string) { return "", "" },
		Codex:          func() map[string]codexsettings.Provider { return nil },
	})
}

func byProvider(creds []Credential) map[string]Credential {
	out := map[string]Credential{}
	for _, c := range creds {
		out[c.ProviderID] = c
	}
	return out
}

func TestOpencodeDBCountsTheActiveGoKeyAndAPlainOAuthLogin(t *testing.T) {
	data := t.TempDir()
	future := time.Now().Add(time.Hour).UnixMilli()
	writeOpencodeDB(t, filepath.Join(data, "opencode.db"), []opencodeRow{
		{"opencode-go", `{"type":"key","key":"TEST-GO-KEY"}`, 1},
		{"opencode-go", `{"type":"key","key":"TEST-GO-OLD"}`, 0},
		{"opencode", `{"type":"key","key":"TEST-ZEN-KEY"}`, 1},
		{"opencode", `{"type":"oauth","access":"TEST-ACCESS","expires":` + strconv.FormatInt(future, 10) + `}`, 1},
		{"unrelated", `{"type":"key","key":"TEST-OTHER"}`, 1},
	})
	got := byProvider(findOpencode(t, data, nil))
	if got["opencode-go"].Key != "TEST-GO-KEY" || got["opencode-go"].Origin != "opencode.db" {
		t.Errorf("go key %+v", got["opencode-go"])
	}
	if got["opencode"].Key != "TEST-ACCESS" {
		t.Errorf("oauth login %+v", got["opencode"])
	}
	for _, c := range findOpencode(t, data, nil) {
		if strings.Contains(c.Key, "TEST-ZEN") || strings.Contains(c.Key, "TEST-GO-OLD") || strings.Contains(c.Key, "TEST-OTHER") {
			t.Errorf("counted a row that does not count: %+v", c)
		}
	}
}

func TestOpencodeDBSkipsAnExpiredOAuthLogin(t *testing.T) {
	data := t.TempDir()
	past := time.Now().Add(-time.Hour).UnixMilli()
	writeOpencodeDB(t, filepath.Join(data, "opencode.db"), []opencodeRow{
		{"opencode", `{"type":"oauth","access":"TEST-EXPIRED","expires":` + strconv.FormatInt(past, 10) + `}`, 1},
	})
	if _, ok := byProvider(findOpencode(t, data, nil))["opencode"]; ok {
		t.Error("an expired login was used; a token is never refreshed here")
	}
}

// auth.json stays the fallback for providers the database does not name.
func TestOpencodeAuthJSONIsTheFallback(t *testing.T) {
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "auth.json"), []byte(`{
		"opencode-go": {"type": "api", "key": "TEST-JSON-GO"},
		"openrouter": {"type": "api", "key": "TEST-JSON-OR"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeOpencodeDB(t, filepath.Join(data, "opencode.db"), []opencodeRow{
		{"opencode-go", `{"type":"key","key":"TEST-GO-KEY"}`, 1},
	})
	creds := findOpencode(t, data, nil)
	got := byProvider(creds)
	if got["opencode-go"].Key != "TEST-GO-KEY" {
		t.Errorf("the database did not win: %+v", got["opencode-go"])
	}
	if got["openrouter"].Key != "TEST-JSON-OR" || got["openrouter"].Origin != "opencode auth.json" {
		t.Errorf("auth.json fallback lost: %+v", got["openrouter"])
	}
	if strings.Contains(getOrigin(creds), "TEST-") {
		t.Error("an origin carries a key")
	}
}

// Without a credential table, the database is no source at all and
// auth.json is read as before.
func TestOpencodeDBWithoutCredentialTableFallsBack(t *testing.T) {
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "auth.json"), []byte(`{"openrouter": {"type": "api", "key": "TEST-JSON-OR"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(data, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE message (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if got := byProvider(findOpencode(t, data, nil))["openrouter"]; got.Key != "TEST-JSON-OR" {
		t.Errorf("auth.json not read: %+v", got)
	}
}

// OPENCODE_DB names the database, as opencode 2 honours it.
func TestOpencodeDBHonoursOPENCODE_DB(t *testing.T) {
	data, elsewhere := t.TempDir(), t.TempDir()
	writeOpencodeDB(t, filepath.Join(elsewhere, "moved.db"), []opencodeRow{
		{"opencode-go", `{"type":"key","key":"TEST-MOVED"}`, 1},
	})
	env := map[string]string{"OPENCODE_DB": filepath.Join(elsewhere, "moved.db")}
	if got := byProvider(findOpencode(t, data, env))["opencode-go"]; got.Key != "TEST-MOVED" {
		t.Errorf("OPENCODE_DB ignored: %+v", got)
	}
}

func getOrigin(creds []Credential) string {
	var b strings.Builder
	for _, c := range creds {
		b.WriteString(c.Origin + "\n")
	}
	return b.String()
}
