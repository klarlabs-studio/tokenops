package fmtindex

import (
	"os"
	"testing"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/optimization/fmtlearn"
)

func TestAppendRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	recs := []fmtlearn.Record{
		{Type: fmtlearn.RecordCompress, ID: "a", Command: "go", RawBytes: 1000, TokensSaved: 200, Handled: true, TS: time.Unix(1, 0)},
		{Type: fmtlearn.RecordAccess, ID: "a", Command: "go", TS: time.Unix(2, 0)},
	}
	for _, r := range recs {
		if err := Append(dir, r); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 records, got %d", len(got))
	}
	if got[0].ID != "a" || got[0].Command != "go" || got[0].RawBytes != 1000 {
		t.Errorf("compress record mismatch: %+v", got[0])
	}
	if got[1].Type != fmtlearn.RecordAccess {
		t.Errorf("access record mismatch: %+v", got[1])
	}
}

func TestRead_MissingIndexIsEmpty(t *testing.T) {
	got, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("missing index should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %d", len(got))
	}
}

func TestRead_SkipsMalformedRows(t *testing.T) {
	dir := t.TempDir()
	// Write a good record, then the reader should tolerate a bad line.
	if err := Append(dir, fmtlearn.Record{Type: fmtlearn.RecordCompress, ID: "x", Command: "npm", TS: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	path, _ := Path(dir)
	// Append a malformed line directly.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{not json}\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 || got[0].ID != "x" {
		t.Errorf("malformed row not skipped cleanly: %+v", got)
	}
}
