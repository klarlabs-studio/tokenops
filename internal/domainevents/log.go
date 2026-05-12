package domainevents

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Record is the on-disk envelope a JSONLog persists per published
// event. The encoding is deliberately loose so unknown event kinds
// (added later) round-trip without breaking older replayers.
type Record struct {
	Kind    string          `json:"kind"`
	At      time.Time       `json:"at"`
	Payload json.RawMessage `json:"payload"`
}

// JSONLog is an append-only on-disk log of every domain event the bus
// publishes. Subscribers that join after startup can call Replay to
// reconstruct prior state. Concurrency safe.
//
// When MaxBytes > 0 the log rotates on overflow: the current file is
// renamed to <path>.1 (older rotations shifted to .2, .3, … up to
// MaxBackups). Default rotation cap when constructed via NewJSONLog is
// 10 MiB, 3 backups — adjust via NewJSONLogWithRotation.
type JSONLog struct {
	mu         sync.Mutex
	file       *os.File
	enc        *json.Encoder
	path       string
	maxBytes   int64
	maxBackups int
}

// NewJSONLog opens (or creates) the log at path in append mode with
// default rotation (10 MiB, 3 backups).
func NewJSONLog(path string) (*JSONLog, error) {
	return NewJSONLogWithRotation(path, 10<<20, 3)
}

// NewJSONLogWithRotation opens the log with explicit rotation
// parameters. maxBytes <= 0 disables rotation; maxBackups <= 0 keeps a
// single rolled file.
func NewJSONLogWithRotation(path string, maxBytes int64, maxBackups int) (*JSONLog, error) {
	if path == "" {
		return nil, errors.New("domainevents: JSONLog requires a path")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("domainevents: open log %s: %w", path, err)
	}
	return &JSONLog{
		file:       f,
		enc:        json.NewEncoder(f),
		path:       path,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
	}, nil
}

// rotate closes the current file, shifts backups, opens a fresh file.
// Caller must hold l.mu.
func (l *JSONLog) rotate() error {
	if l.maxBytes <= 0 || l.maxBackups <= 0 {
		return nil
	}
	if err := l.file.Close(); err != nil {
		return err
	}
	for i := l.maxBackups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1))
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	l.file = f
	l.enc = json.NewEncoder(f)
	return nil
}

// Attach subscribes the log to bus. Every published event is written
// as one JSON object per line. Disk errors are surfaced via the
// supplied error channel when non-nil; otherwise silently dropped.
func (l *JSONLog) Attach(bus *Bus, errs chan<- error) {
	bus.Subscribe("*", func(ev Event) {
		payload, err := json.Marshal(ev)
		if err != nil {
			if errs != nil {
				select {
				case errs <- err:
				default:
				}
			}
			return
		}
		rec := Record{Kind: ev.Kind(), At: time.Now().UTC(), Payload: payload}
		l.mu.Lock()
		if l.maxBytes > 0 {
			if info, statErr := l.file.Stat(); statErr == nil && info.Size() >= l.maxBytes {
				if rerr := l.rotate(); rerr != nil && errs != nil {
					select {
					case errs <- rerr:
					default:
					}
				}
			}
		}
		err = l.enc.Encode(rec)
		l.mu.Unlock()
		if err != nil && errs != nil {
			select {
			case errs <- err:
			default:
			}
		}
	})
}

// Close flushes (fsync) and closes the underlying file. Idempotent.
func (l *JSONLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	_ = l.file.Sync()
	err := l.file.Close()
	l.file = nil
	l.enc = nil
	return err
}

// Replay scans the log file and invokes fn for every record. The
// caller decides how to dispatch — typically by re-publishing on a
// fresh bus, or by hydrating a subscriber's internal state. Stops on
// the first fn error. Malformed records fail fast; use ReplayLenient
// to skip them.
//
// Concurrency note: Replay opens its own read-only handle and uses
// bufio.Scanner. A partially-written final line (writer flushed before
// fsync) will be returned by Scanner with the bytes available; the
// JSON decoder may then error. Use ReplayLenient when readers can race
// with active writers.
func Replay(path string, fn func(Record) error) error {
	return replay(path, fn, false)
}

// ReplayLenient is Replay that skips malformed JSON lines rather than
// aborting. malformed lines are tallied via the returned count.
func ReplayLenient(path string, fn func(Record) error) (skipped int, err error) {
	err = replay(path, func(r Record) error {
		return fn(r)
	}, true)
	skipped = lenientSkips
	lenientSkips = 0
	return
}

// lenientSkips is reset by ReplayLenient — package-local state because
// the encoder/decoder doesn't surface line errors otherwise.
var lenientSkips int

func replay(path string, fn func(Record) error, lenient bool) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for scanner.Scan() {
		var rec Record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			if lenient {
				lenientSkips++
				continue
			}
			return fmt.Errorf("domainevents: decode record: %w", err)
		}
		if err := fn(rec); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// ReplayInto re-publishes every record on the given bus by wrapping
// the raw payload in a replayedEvent that preserves Kind. Subscribers
// see the original Kind string; type-asserting subscribers receive a
// replayedEvent value rather than the original concrete type.
func ReplayInto(path string, bus *Bus) error {
	return Replay(path, func(r Record) error {
		bus.Publish(replayedEvent{kind: r.Kind, payload: r.Payload})
		return nil
	})
}

// replayedEvent is the synthetic Event a Replay produces. It is the
// minimal Event satisfier — subscribers that need the typed payload
// must json.Unmarshal Payload themselves.
type replayedEvent struct {
	kind    string
	at      time.Time
	payload json.RawMessage
}

func (r replayedEvent) Kind() string                 { return r.kind }
func (r replayedEvent) At() time.Time                { return r.at }
func (r replayedEvent) Payload() json.RawMessage     { return r.payload }
func (r replayedEvent) MarshalJSON() ([]byte, error) { return r.payload, nil }

// NewReplayed builds a synthetic Event for hydration paths that only
// need to carry the kind through subscribers (e.g. an in-memory event
// counter). Payload is left empty.
func NewReplayed(kind string, at time.Time) Event {
	return replayedEvent{kind: kind, at: at}
}

// Ensure io.Closer + json.Marshaler are satisfied where expected.
var _ io.Closer = (*JSONLog)(nil)
