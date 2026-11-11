package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// A command that only reads says when nothing has been recorded, and
// leaves no store behind: opened read-write, each made an empty one and
// answered from it as if nothing had happened.
func TestReadCommandsDoNotCreateTheStore(t *testing.T) {
	for _, args := range [][]string{
		{"spend"},
		{"spend", "units"},
		{"spend", "waste"},
		{"events"},
		{"audit"},
		{"replay"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "events.db")
			_, err := executeRoot(t, append(args, "--db", path)...)
			if err == nil || !strings.Contains(err.Error(), "no event store at "+path) {
				t.Errorf("error = %v, want it to say there is no event store", err)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("a read created %s", path)
			}
		})
	}
}

// What a read command opens takes no write: the daemon's appends are not
// held behind it.
func TestOpenStoreForReadIsReadOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.db")
	rw, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = rw.Close()
	s, err := openStoreForRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	env := &eventschema.Envelope{ID: "x", SchemaVersion: eventschema.SchemaVersion, Type: eventschema.EventTypePrompt,
		Timestamp: time.Now(), Source: "test", Payload: &eventschema.PromptEvent{Provider: eventschema.ProviderAnthropic}}
	if err := s.Append(ctx, env); err == nil {
		t.Error("the store a read command opened accepted a write")
	}
}
