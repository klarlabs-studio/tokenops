package claudecodejsonl

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func assistantLine(id, ts string) string {
	return `{"type":"assistant","timestamp":"` + ts + `","sessionId":"s1","message":{"id":"` + id + `","model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":20}}}`
}

func userPromptLine(ts string) string {
	return `{"type":"user","timestamp":"` + ts + `","sessionId":"s1","message":{"content":"do the thing"}}`
}

// incrementalFixture is one session file under a fresh root plus a poller
// watching it.
type incrementalFixture struct {
	t    *testing.T
	root string
	path string
	bus  *captureBus
	p    *Poller
}

func newIncrementalFixture(t *testing.T) *incrementalFixture {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	bus := &captureBus{}
	return &incrementalFixture{
		t:    t,
		root: root,
		path: filepath.Join(proj, "sess.jsonl"),
		bus:  bus,
		p:    NewPoller(bus, PollerOptions{Root: root, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}),
	}
}

func (f *incrementalFixture) write(s string) {
	f.t.Helper()
	if err := os.WriteFile(f.path, []byte(s), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *incrementalFixture) appendText(s string) {
	f.t.Helper()
	fh, err := os.OpenFile(f.path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = fh.Close() }()
	if _, err := fh.WriteString(s); err != nil {
		f.t.Fatal(err)
	}
}

func (f *incrementalFixture) scan() { f.p.scan(context.Background(), f.root) }

func (f *incrementalFixture) published() []*eventschema.Envelope {
	f.bus.mu.Lock()
	defer f.bus.mu.Unlock()
	return append([]*eventschema.Envelope(nil), f.bus.envelopes...)
}

// The poller reads transcripts through a jsonltail.Tail, so a scan parses
// only what was appended since the last one. The general cases — partial
// lines, replaced files, oversized lines — are pinned in jsonltail.
func TestScanParsesOnlyAppendedBytes(t *testing.T) {
	f := newIncrementalFixture(t)
	first := assistantLine("msg_1", "2026-09-19T10:00:00Z") + "\n" + assistantLine("msg_2", "2026-09-19T10:00:05Z") + "\n"
	f.write(first)

	f.scan()
	if got := f.p.tail.BytesParsed; got != int64(len(first)) {
		t.Fatalf("first scan parsed %d bytes, want the whole file (%d)", got, len(first))
	}

	f.scan()
	if got := f.p.tail.FilesOpened; got != 1 {
		t.Fatalf("opened the transcript %d times across two scans with no change, want 1", got)
	}

	appended := assistantLine("msg_3", "2026-09-19T10:00:10Z") + "\n"
	f.appendText(appended)
	f.scan()
	if got, want := f.p.tail.BytesParsed, int64(len(first)+len(appended)); got != want {
		t.Fatalf("after an append parsed %d bytes total, want %d (only the new line)", got, want)
	}
	if got := len(f.published()); got != 3 {
		t.Fatalf("published %d turns, want 3", got)
	}
}

// The reader times a turn against the entry before it and marks the first
// turn after a typed prompt. Both depend on lines from an earlier scan when
// the file is read in pieces, so that state has to survive between scans.
func TestScanCarriesTurnStateAcrossReads(t *testing.T) {
	f := newIncrementalFixture(t)
	f.write(userPromptLine("2026-09-19T10:00:00Z") + "\n")
	f.scan()
	if got := len(f.published()); got != 0 {
		t.Fatalf("a prompt alone published %d turns", got)
	}

	f.appendText(assistantLine("msg_1", "2026-09-19T10:00:07Z") + "\n")
	f.scan()
	got := f.published()
	if len(got) != 1 {
		t.Fatalf("published %d turns, want 1", len(got))
	}
	if v := got[0].Attributes["starts_user_message"]; v != "true" {
		t.Errorf("starts_user_message = %q, want true: the prompt was read in the previous scan", v)
	}
	pe, ok := got[0].Payload.(*eventschema.PromptEvent)
	if !ok {
		t.Fatalf("payload is %T", got[0].Payload)
	}
	if pe.Latency != 7*time.Second {
		t.Errorf("latency = %s, want 7s measured from the prompt read in the previous scan", pe.Latency)
	}
}
