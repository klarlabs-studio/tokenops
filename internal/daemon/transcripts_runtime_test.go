package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
)

// filledTranscriptCache is a cache that has read one transcript.
func filledTranscriptCache(t *testing.T) *sessions.TranscriptCache {
	t.Helper()
	root := t.TempDir()
	at := time.Now().Add(-time.Hour).UTC()
	body := fmt.Sprintf(`{"type":"user","timestamp":%q,"sessionId":"s","message":{"content":"task"}}`+"\n"+
		`{"type":"assistant","timestamp":%q,"sessionId":"s","message":{"model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":5}}}`+"\n",
		at.Format(time.RFC3339Nano), at.Add(time.Second).Format(time.RFC3339Nano))
	if err := os.MkdirAll(filepath.Join(root, "p"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "p", "s.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := sessions.NewTranscriptCache()
	if _, err := agentdx.Extract(agentdx.ExtractOptions{Root: root, Since: at.Add(-time.Hour), IncludeScratch: true, Cache: c}); err != nil {
		t.Fatal(err)
	}
	if c.Len() != 1 {
		t.Fatalf("the cache holds %d files; want 1", c.Len())
	}
	return c
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// The key is of the code that reads transcripts, so two releases that
// read them alike share a save. A build that cannot say what it was made
// from has none.
func TestTranscriptsKey(t *testing.T) {
	for name, tt := range map[string]struct {
		ver, commit string
		want        string
	}{
		"a release":                    {"0.107.0", "abc1234", sessions.TranscriptsRevision},
		"the next release":             {"0.108.0", "def5678", sessions.TranscriptsRevision},
		"a build between releases":     {"v0.107.0-3-gabc1234", "abc1234", sessions.TranscriptsRevision},
		"no commit":                    {"0.107.0", "", ""},
		"go build, nothing stamped":    {"dev", "unknown", ""},
		"a tree with uncommitted work": {"v0.107.0-3-gabc1234-dirty", "abc1234", ""},
	} {
		if got := transcriptsKeyFor(tt.ver, tt.commit); got != tt.want {
			t.Errorf("%s: key %q, want %q", name, got, tt.want)
		}
	}
	if sessions.TranscriptsRevision == "" {
		t.Fatal("no revision: every build would be one that saves nothing")
	}
}

// An upgrade that leaves the reading code alone starts from the last
// release's save.
func TestTranscriptCacheOutlivesAnUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "transcripts.gob")
	if !saveTranscripts(filledTranscriptCache(t), path, transcriptsKeyFor("0.107.0", "abc1234"), quiet()) {
		t.Fatal("not saved")
	}
	next := sessions.NewTranscriptCache()
	if !loadTranscripts(next, path, transcriptsKeyFor("0.108.0", "def5678"), quiet()) || next.Len() != 1 {
		t.Fatalf("the next release restored %d files; want the 1 saved", next.Len())
	}
	// Other reading code, no save, or a build with no key: nothing is
	// restored and the start says so.
	for name, tt := range map[string]struct{ path, key string }{
		"other reading code":  {path, "another revision"},
		"no save":             {filepath.Join(t.TempDir(), "transcripts.gob"), sessions.TranscriptsRevision},
		"a build with no key": {path, ""},
	} {
		c := sessions.NewTranscriptCache()
		if loadTranscripts(c, tt.path, tt.key, quiet()) || c.Len() != 0 {
			t.Errorf("%s: restored %d files", name, c.Len())
		}
	}
	if saveTranscripts(filledTranscriptCache(t), filepath.Join(t.TempDir(), "transcripts.gob"), "", quiet()) {
		t.Error("a build with no key saved")
	}
	// Not even a save that carries no key, as one from such a build
	// would.
	unkeyed := filepath.Join(t.TempDir(), "transcripts.gob")
	if err := filledTranscriptCache(t).Save(unkeyed, ""); err != nil {
		t.Fatal(err)
	}
	if c := sessions.NewTranscriptCache(); loadTranscripts(c, unkeyed, "", quiet()) || c.Len() != 0 {
		t.Errorf("a build with no key restored %d files", c.Len())
	}
}

// saveLoop runs the save loop until the test ends.
func saveLoop(t *testing.T, c *sessions.TranscriptCache, path string, saved time.Time, every time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runTranscriptSaves(ctx, c, path, "r1", saved, time.Millisecond, every, quiet())
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the loop did not stop")
		}
	})
}

func appears(path string, within time.Duration) bool {
	for end := time.Now().Add(within); time.Now().Before(end); time.Sleep(time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// A running daemon saves when its save is older than a day, or was never
// made, and not otherwise.
func TestTranscriptSavesWhileRunning(t *testing.T) {
	const quietFor = 60 * time.Millisecond // sixty looks
	for name, tt := range map[string]struct {
		empty bool
		saved time.Duration // how long ago; 0 is never
		every time.Duration
		want  bool
	}{
		"never saved":            {every: time.Hour, want: true},
		"saved longer ago":       {saved: 2 * time.Hour, every: time.Hour, want: true},
		"saved at start":         {saved: time.Nanosecond, every: time.Hour},
		"saved just short of it": {saved: 59 * time.Minute, every: time.Hour},
		"nothing read yet":       {empty: true, every: time.Hour},
		"nothing read, old save": {empty: true, saved: 2 * time.Hour, every: time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			c := sessions.NewTranscriptCache()
			if !tt.empty {
				c = filledTranscriptCache(t)
			}
			var saved time.Time
			if tt.saved > 0 {
				saved = time.Now().Add(-tt.saved)
			}
			path := filepath.Join(t.TempDir(), "transcripts.gob")
			saveLoop(t, c, path, saved, tt.every)
			within := quietFor
			if tt.want {
				within = 5 * time.Second
			}
			if got := appears(path, within); got != tt.want {
				t.Fatalf("saved = %v, want %v", got, tt.want)
			}
		})
	}
}

// One save holds for the whole period: the loop does not write the cache
// at every look.
func TestTranscriptSaveIsNotRepeatedWithinThePeriod(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.gob")
	saveLoop(t, filledTranscriptCache(t), path, time.Time{}, time.Hour)
	if !appears(path, 5*time.Second) {
		t.Fatal("never saved")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if appears(path, 60*time.Millisecond) {
		t.Fatal("saved again within the period")
	}
}

// And it is made again once the period is over.
func TestTranscriptSaveIsRepeatedAfterThePeriod(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcripts.gob")
	saveLoop(t, filledTranscriptCache(t), path, time.Time{}, 20*time.Millisecond)
	for i := range 3 {
		if !appears(path, 5*time.Second) {
			t.Fatalf("save %d never made", i+1)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

// A save that failed is tried again at the next look, not a period
// later.
func TestTranscriptSaveThatFailedIsTriedAgain(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "state")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocked, "transcripts.gob")
	saveLoop(t, filledTranscriptCache(t), path, time.Time{}, time.Hour)
	if appears(path, 30*time.Millisecond) {
		t.Fatal("saved under a file")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if !appears(path, 5*time.Second) {
		t.Fatal("not tried again once the directory could be made")
	}
}
