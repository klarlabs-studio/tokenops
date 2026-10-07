package agentdx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ErrCursorSchema reports that a Cursor database was found but did not
// contain the tables this reader knows how to read.
//
// It is a distinct error rather than an empty result on purpose. Cursor's
// storage is undocumented and has changed shape before; if a future
// version moves the keys, an operator must be told the reader broke
// rather than shown a report claiming they did no work. Every metric that
// was silently wrong before this — a window meter reading 0/200 under
// full load, compactions reading zero on a corpus full of them — failed
// exactly that way: a read failure rendered as an absence of activity.
var ErrCursorSchema = errors.New("agentdx: unrecognised Cursor database schema")

// ErrNoCursorStore reports a Cursor store on disk that this process has
// no reader for: a composition root that did not call UseCursorStore. It
// is an error rather than an empty result for the same reason as
// ErrCursorSchema — an unread store must not read as an idle operator.
var ErrNoCursorStore = errors.New("agentdx: no reader for Cursor's store is wired")

// CursorStore reads Cursor's chat store, the VS Code-style SQLite
// key-value database at globalStorage/state.vscdb. The SQL lives behind
// it, in internal/infra/cursorstate; what the rows mean stays here.
type CursorStore interface {
	// Bubbles calls yield with the key and JSON value of every
	// bubbleId:* row of the store at path, opened read-only so a running
	// Cursor is never disturbed. A row it cannot scan is skipped. An
	// error wrapping ErrCursorSchema means the store is not in a shape
	// it knows how to read.
	Bubbles(path string, yield func(key, value string)) error
}

// cursorStore is the reader ExtractCursor uses; see UseCursorStore.
var cursorStore CursorStore

// UseCursorStore installs the reader for Cursor's store. The composition
// root (internal/bootstrap) installs internal/infra/cursorstate before
// anything extracts; without one, a Cursor store on disk is reported as
// ErrNoCursorStore.
func UseCursorStore(s CursorStore) { cursorStore = s }

// CursorDefaultRoot returns Cursor's user-data directory.
func CursorDefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Cursor", "User"), nil
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Cursor", "User"), nil
	default:
		return filepath.Join(home, ".config", "Cursor", "User"), nil
	}
}

// bubbleTypes are Cursor's message roles. 1 is what the operator typed,
// 2 is the model's reply.
const (
	cursorBubbleUser      = 1
	cursorBubbleAssistant = 2
)

// cursorBubble is one message row from cursorDiskKV.
type cursorBubble struct {
	Type       int        `json:"type"`
	Text       string     `json:"text"`
	CreatedAt  cursorTime `json:"createdAt"`
	TokenCount struct {
		InputTokens int64 `json:"inputTokens"`
	} `json:"tokenCount"`
	ToolFormerData *struct {
		Name string `json:"name"`
	} `json:"toolFormerData,omitempty"`
}

// ExtractCursor reads Cursor's chat store into Records.
//
// Cursor keeps conversations in a VS Code-style SQLite key-value store:
// globalStorage/state.vscdb holds a cursorDiskKV table whose rows are
// keyed bubbleId:<composerId>:<bubbleId>, one per message. The composer
// id is the session.
//
// A missing store is not an error — most operators do not run Cursor, and
// reporting a failure for that would make the common case look broken.
// A store that exists but cannot be understood IS an error; see
// ErrCursorSchema.
func ExtractCursor(opts ExtractOptions) ([]Record, error) {
	root := opts.Root
	if root == "" {
		r, err := CursorDefaultRoot()
		if err != nil {
			return nil, err
		}
		root = r
	}
	dbPath := filepath.Join(root, "globalStorage", "state.vscdb")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil
	}

	store := cursorStore
	if store == nil {
		return nil, fmt.Errorf("%w: found %s", ErrNoCursorStore, dbPath)
	}

	var (
		out     []Record
		scanned int
		// parsed counts bubbles in a known shape, inside the window or
		// not; sample keeps one timestamp that could not be read.
		parsed int
		sample string
	)
	err := store.Bubbles(dbPath, func(key, value string) {
		scanned++
		var b cursorBubble
		if json.Unmarshal([]byte(value), &b) != nil {
			return
		}
		at := b.CreatedAt.at
		if at.IsZero() {
			if sample == "" {
				sample = b.CreatedAt.raw
			}
			return
		}
		parsed++
		if !opts.Since.IsZero() && at.Before(opts.Since) {
			return
		}
		rec := Record{At: at, SessionID: composerFromKey(key)}

		switch {
		case b.ToolFormerData != nil && b.ToolFormerData.Name != "":
			rec.Kind = KindToolUse
			rec.ToolName = b.ToolFormerData.Name
		case b.Type == cursorBubbleUser:
			if strings.TrimSpace(b.Text) == "" {
				return
			}
			rec.Kind = KindPrompt
			// The words are already parsed for the empty check above;
			// carrying them is what lets `tokenops story` title a Cursor
			// task with the operator's own instruction instead of
			// "(no instruction text)".
			if opts.WithPromptText {
				rec.Text = b.Text
			}
		case b.Type == cursorBubbleAssistant:
			rec.Kind = KindAssistantTurn
			rec.InputTokens = b.TokenCount.InputTokens
		default:
			return
		}
		out = append(out, rec)
	})
	if err != nil {
		return nil, err
	}
	// Rows keyed as bubbles that none of them parsed means the value
	// shape moved, not that the operator was idle. Bubbles that parsed but
	// fall outside the window are an operator who did not use Cursor
	// lately, which is not a schema failure.
	if scanned > 0 && parsed == 0 {
		hint := ""
		if sample != "" {
			hint = fmt.Sprintf(" (createdAt looks like %q)", sample)
		}
		return nil, fmt.Errorf("%w: %d bubble rows in %s, none in a known shape%s",
			ErrCursorSchema, scanned, dbPath, hint)
	}
	return out, nil
}

// composerFromKey pulls the session id out of bubbleId:<composer>:<bubble>.
func composerFromKey(key string) string {
	parts := strings.Split(key, ":")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// cursorTime is a bubble's createdAt. Cursor has written it as epoch
// milliseconds and, in current builds, as a string. Every shape seen or
// plausible is accepted: a number or numeric string in seconds or
// milliseconds, or a date-time with or without a zone. raw keeps what
// could not be read, for the schema error to show.
type cursorTime struct {
	at  time.Time
	raw string
}

// cursorLayouts are the date-time forms accepted for a string createdAt.
var cursorLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	time.RFC1123Z,
	time.RFC1123,
}

// UnmarshalJSON never fails: an unreadable value is the zero time, which
// the reader skips and reports.
func (t *cursorTime) UnmarshalJSON(b []byte) error {
	var n float64
	if json.Unmarshal(b, &n) == nil {
		t.at = epoch(n)
		if t.at.IsZero() {
			t.raw = string(b)
		}
		return nil
	}
	var s string
	if json.Unmarshal(b, &s) != nil {
		t.raw = string(b)
		return nil
	}
	s = strings.TrimSpace(s)
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		t.at = epoch(f)
	} else {
		for _, layout := range cursorLayouts {
			if at, err := time.Parse(layout, s); err == nil {
				t.at = at.UTC()
				break
			}
		}
	}
	if t.at.IsZero() {
		t.raw = s
		if len(t.raw) > 40 {
			t.raw = t.raw[:40]
		}
	}
	return nil
}

// epoch reads a Unix time in seconds or milliseconds: anything below
// 1e11 is seconds (1e11 ms is 1973, 1e11 s is the year 5138).
func epoch(n float64) time.Time {
	switch {
	case n <= 0:
		return time.Time{}
	case n < 1e11:
		return time.Unix(int64(n), 0).UTC()
	default:
		return time.UnixMilli(int64(n)).UTC()
	}
}
