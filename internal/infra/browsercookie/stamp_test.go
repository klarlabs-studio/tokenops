package browsercookie

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The fingerprint changes when the browser writes its cookie store (the
// file or its write-ahead log) and stays the same otherwise, without
// opening either file.
func TestStampChangesOnlyWhenTheCookieStoreDoes(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, "Library/Application Support/Google/Chrome/Default")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(profile, "Cookies")
	write := func(path, body string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	write(store, "v1", t0)

	tests := []struct {
		name   string
		change func()
		want   bool // whether the stamp changes
	}{
		{"nothing written", func() {}, false},
		{"store rewritten", func() { write(store, "v2", t0.Add(time.Minute)) }, true},
		{"write-ahead log appears", func() { write(store+"-wal", "w", t0.Add(2*time.Minute)) }, true},
		{"write-ahead log grows", func() { write(store+"-wal", "ww", t0.Add(2*time.Minute)) }, true},
		{"read again", func() {}, false},
	}
	before := Stamp(home, "Chrome")
	if before == "" {
		t.Fatal("no stamp for a store that exists")
	}
	for _, tt := range tests {
		tt.change()
		after := Stamp(home, "Chrome")
		if got := after != before; got != tt.want {
			t.Errorf("%s: stamp changed = %v, want %v", tt.name, got, tt.want)
		}
		before = after
	}
}

// With no browser installed there is nothing to watch, and the caller
// reads as it always did.
func TestStampIsEmptyWithoutABrowser(t *testing.T) {
	if s := Stamp(t.TempDir(), ""); s != "" {
		t.Errorf("stamp %q with no browser", s)
	}
}
