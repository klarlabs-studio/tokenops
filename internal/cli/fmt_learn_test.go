package cli

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn"
)

func TestLearnIndex_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	res := &fmtResult{
		RecoveryID:   "20260101T000000-deadbeef",
		BytesBefore:  1000,
		BytesAfter:   400,
		Handled:      true,
		CriticalKept: true,
	}
	if err := recordCompressRun(dir, "go", "balanced", res, time.Unix(0, 0)); err != nil {
		t.Fatalf("recordCompressRun: %v", err)
	}
	recs, err := readLearnRecords(dir)
	if err != nil {
		t.Fatalf("readLearnRecords: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	r := recs[0]
	if r.Type != fmtlearn.RecordCompress || r.Command != "go" || r.ID != res.RecoveryID {
		t.Errorf("unexpected record: %+v", r)
	}
	if r.TokensSaved != 150 { // (1000-400)/4
		t.Errorf("tokens saved = %d, want 150", r.TokensSaved)
	}
	if !r.Handled || r.GenericFallback {
		t.Errorf("handled run should not be a generic fallback: %+v", r)
	}
}

func TestLearnIndex_NoRecoveryIDSkips(t *testing.T) {
	dir := t.TempDir()
	// Recovery disabled -> no RecoveryID -> no record written.
	if err := recordCompressRun(dir, "go", "balanced", &fmtResult{}, time.Unix(0, 0)); err != nil {
		t.Fatalf("recordCompressRun: %v", err)
	}
	recs, _ := readLearnRecords(dir)
	if len(recs) != 0 {
		t.Errorf("expected no record when RecoveryID empty, got %d", len(recs))
	}
}

func TestLearnIndex_MissingIndexIsEmpty(t *testing.T) {
	recs, err := readLearnRecords(t.TempDir())
	if err != nil {
		t.Fatalf("missing index should not error: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("want empty, got %d", len(recs))
	}
}

func TestLearnIndex_GenericFallbackFlagged(t *testing.T) {
	dir := t.TempDir()
	res := &fmtResult{RecoveryID: "id1", BytesBefore: 500, BytesAfter: 480, Handled: false}
	_ = recordCompressRun(dir, "jq", "balanced", res, time.Unix(0, 0))
	recs, _ := readLearnRecords(dir)
	if len(recs) != 1 || !recs[0].GenericFallback {
		t.Errorf("unhandled command must be flagged generic_fallback: %+v", recs)
	}
}
