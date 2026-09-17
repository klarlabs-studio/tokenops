package cursorturns

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func osOpenAppend(dir string) (*os.File, error) {
	return os.OpenFile(filepath.Join(dir, "turns.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}

func p64(v int64) *int64 { return &v }

// The hook payload is the only place Cursor's per-turn tokens ever
// exist. Round-tripping them through the ledger is what upgrades Cursor
// from quota-only spend.
func TestAppendAndReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	Append(dir, Turn{
		TS: time.Now().UTC(), ConversationID: "c1", GenerationID: "g1",
		ModelID: "grok-4.6", InputTokens: p64(100), OutputTokens: p64(10),
	})
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d turns, want 1", len(got))
	}
	if got[0].GenerationID != "g1" || *got[0].InputTokens != 100 {
		t.Errorf("round trip lost data: %+v", got[0])
	}
}

// A turn Cursor did not measure must not become a line that later
// becomes an event asserting it cost nothing.
func TestAppendSkipsUnmeasuredTurns(t *testing.T) {
	dir := t.TempDir()
	Append(dir, Turn{ConversationID: "c1", GenerationID: "g1", ModelID: "m"})
	got, _ := Read(dir)
	if len(got) != 0 {
		t.Errorf("an unmeasured turn was recorded: %+v", got)
	}
}

// Reading a ledger that does not exist is the normal state on a machine
// that has never run Cursor, not an error.
func TestReadMissingLedgerIsNotAnError(t *testing.T) {
	got, err := Read(t.TempDir())
	if err != nil || got != nil {
		t.Errorf("Read on an empty dir = %v, %v; want nil, nil", got, err)
	}
}

// One malformed line must not lose the rest of the ledger.
func TestReadSurvivesAMalformedLine(t *testing.T) {
	dir := t.TempDir()
	Append(dir, Turn{GenerationID: "g1", ModelID: "m", InputTokens: p64(1)})
	appendRaw(t, dir, "{not json")
	Append(dir, Turn{GenerationID: "g2", ModelID: "m", InputTokens: p64(2)})
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d turns, want 2 — a bad line lost good ones", len(got))
	}
}

func appendRaw(t *testing.T, dir, line string) {
	t.Helper()
	f, err := osOpenAppend(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}
