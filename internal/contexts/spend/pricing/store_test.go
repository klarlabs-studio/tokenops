package pricing

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func snap(source string, at time.Time, rates map[string]Rate) Snapshot {
	return Snapshot{Source: source, SourceURL: "http://x", FetchedAt: at, Rates: rates}
}

func TestSaveAndLoadSnapshots_SortedByFetchedAt(t *testing.T) {
	dir := t.TempDir()
	t2 := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 7, 9, 9, 0, 0, 0, time.UTC)

	// Save out of order.
	for _, s := range []Snapshot{
		snap("litellm", t2, map[string]Rate{"m": {InputPerMillion: 2}}),
		snap("litellm", t1, map[string]Rate{"m": {InputPerMillion: 1}}),
		snap("litellm", t3, map[string]Rate{"m": {InputPerMillion: 3}}),
	} {
		if _, err := SaveSnapshot(dir, s); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	got := LoadSnapshots(dir)
	if len(got) != 3 {
		t.Fatalf("loaded %d, want 3", len(got))
	}
	if !got[0].FetchedAt.Equal(t1) || !got[2].FetchedAt.Equal(t3) {
		t.Errorf("not sorted ascending: %s .. %s", got[0].FetchedAt, got[2].FetchedAt)
	}
}

func TestLatestSnapshot_FallsBackToBaseline(t *testing.T) {
	dir := t.TempDir()
	s, real := LatestSnapshot(dir)
	if real {
		t.Error("empty dir should report baseline (real=false)")
	}
	if s.Source != SourceEmbeddedBaseline {
		t.Errorf("fallback source = %q, want baseline", s.Source)
	}

	at := time.Date(2026, 7, 9, 9, 0, 0, 0, time.UTC)
	if _, err := SaveSnapshot(dir, snap("litellm", at, map[string]Rate{"m": {InputPerMillion: 3}})); err != nil {
		t.Fatal(err)
	}
	s, real = LatestSnapshot(dir)
	if !real || !s.FetchedAt.Equal(at) {
		t.Errorf("latest = %+v real=%v, want the saved one", s.FetchedAt, real)
	}
}

func TestSaveSnapshot_BaselineIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path, err := SaveSnapshot(dir, BaselineSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Errorf("baseline save should be a no-op, wrote %q", path)
	}
	if len(LoadSnapshots(dir)) != 0 {
		t.Error("baseline must not be persisted")
	}
}

func TestSaveSnapshot_AtomicFilename(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 7, 8, 13, 30, 15, 0, time.UTC)
	path, err := SaveSnapshot(dir, snap("litellm", at, map[string]Rate{"m": {InputPerMillion: 1}}))
	if err != nil {
		t.Fatal(err)
	}
	// Colons must be sanitized out of the filename.
	if base := filepath.Base(path); base != "2026-07-08T13-30-15Z.json" {
		t.Errorf("filename = %q, want colon-free RFC3339", base)
	}
}

func TestFindSnapshot(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 7, 8, 13, 30, 15, 0, time.UTC)
	if _, err := SaveSnapshot(dir, snap("litellm", at, map[string]Rate{"m": {InputPerMillion: 1}})); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		sel     string
		wantSrc string
		wantErr bool
	}{
		{"", "litellm", false},
		{"latest", "litellm", false},
		{"baseline", SourceEmbeddedBaseline, false},
		{"2026-07-08T13-30-15Z", "litellm", false},
		{"2026-07-08", "litellm", false}, // date prefix
		{"2099-01-01T00-00-00Z", "", true},
	}
	for _, c := range cases {
		s, err := FindSnapshot(dir, c.sel)
		if c.wantErr {
			if !errors.Is(err, ErrSnapshotNotFound) {
				t.Errorf("FindSnapshot(%q) err = %v, want ErrSnapshotNotFound", c.sel, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("FindSnapshot(%q) unexpected err: %v", c.sel, err)
			continue
		}
		if s.Source != c.wantSrc {
			t.Errorf("FindSnapshot(%q) source = %q, want %q", c.sel, s.Source, c.wantSrc)
		}
	}
}

func TestLoadSnapshots_MissingDirIsEmpty(t *testing.T) {
	if got := LoadSnapshots(filepath.Join(t.TempDir(), "does-not-exist")); got != nil {
		t.Errorf("missing dir should load empty, got %d", len(got))
	}
}
