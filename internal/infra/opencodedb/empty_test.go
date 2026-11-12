package opencodedb

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// opencode leaves an empty opencode.db behind when it starts and never
// writes a session, and a reset leaves one too. Such a store holds nothing
// to ingest, which is a fact, not an unknown: graded unknown, a machine
// that stopped using opencode weeks ago reported it as a critical, silent
// source on every status. A store holding tables but neither message table
// is different: opencode changed its schema, and the reader can no longer
// tell, so that one stays unknown.
func TestAStoreWithNothingInItIsEmpty(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, path string)
		wantKnown bool
		wantErr   error
	}{
		{
			name: "zero-byte file",
			setup: func(t *testing.T, path string) {
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantKnown: true,
		},
		{
			name: "database with no tables",
			setup: func(t *testing.T, path string) {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				// A pragma that writes the header makes a real, empty
				// database file rather than a zero-byte one.
				if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
					t.Fatal(err)
				}
				_ = db.Close()
			},
			wantKnown: true,
		},
		{
			name: "tables but no message table",
			setup: func(t *testing.T, path string) {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`CREATE TABLE something_else (x TEXT)`); err != nil {
					t.Fatal(err)
				}
				_ = db.Close()
			},
			wantKnown: false,
			wantErr:   ErrSchema,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "opencode.db")
			tc.setup(t, path)

			at, known := Newest(path)
			if known != tc.wantKnown || !at.IsZero() {
				t.Errorf("Newest = %v, %v; want zero, %v", at, known, tc.wantKnown)
			}
			err := Read(path, Options{}, func(Message) error {
				t.Error("visited a message in a store holding none")
				return nil
			})
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Read err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
