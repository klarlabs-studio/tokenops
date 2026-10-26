package boundaryledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/boundaries"
)

// Each session's latest tool calls, for the loop breaker, are a log under
// calls/<session>.jsonl: one line per call, a tool name and a hash of its
// input. The input itself never reaches this package.

// callsDir holds the per-session call logs.
const callsDir = "calls"

// callLogCap is the size past which a call log is cut back to its newest
// window: a few windows of lines.
const callLogCap = 16 << 10

func (d Dir) callLogPath(session string) string {
	return filepath.Join(d.root(), callsDir, sessionKey(session)+".jsonl")
}

// AppendCall adds one call to a session's log. One appended line is one
// write, so parallel tool calls each land; past its cap the log is cut back
// to the newest window, and a call appended while it is cut can be lost.
func (d Dir) AppendCall(session string, c boundaries.ToolCall) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	path := d.callLogPath(session)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // TokenOps' own state, key sanitised
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		f, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // as above
	}
	if err != nil {
		return err
	}
	_, werr := f.Write(append(b, '\n'))
	st, serr := f.Stat()
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	if serr == nil && st.Size() > callLogCap {
		return d.cutCallLog(session)
	}
	return nil
}

func (d Dir) cutCallLog(session string) error {
	calls, err := d.RecentCalls(session)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, c := range calls {
		b, err := json.Marshal(c)
		if err != nil {
			return err
		}
		buf.Write(append(b, '\n'))
	}
	return writeAtomic(d.callLogPath(session), buf.Bytes())
}

// RecentCalls is a session's newest calls, oldest first, at most
// boundaries.LoopWindow of them. A missing log is none; a line a crash cut
// short is skipped.
func (d Dir) RecentCalls(session string) ([]boundaries.ToolCall, error) {
	f, err := os.Open(d.callLogPath(session)) //nolint:gosec // TokenOps' own state, key sanitised
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []boundaries.ToolCall
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var c boundaries.ToolCall
		if json.Unmarshal(sc.Bytes(), &c) != nil || c.Tool == "" {
			continue
		}
		out = append(out, c)
		if len(out) > 2*boundaries.LoopWindow {
			out = append(out[:0], out[len(out)-boundaries.LoopWindow:]...)
		}
	}
	if len(out) > boundaries.LoopWindow {
		out = out[len(out)-boundaries.LoopWindow:]
	}
	return out, sc.Err()
}

// Call logs are kept while their session may still be looping: a log not
// written for callLogIdle belongs to a session with no live loop to see.
// The directory is scanned at most every callPruneEvery, as the stamp
// beside the logs records, so the pause point pays one stat per call.
const (
	callLogIdle    = 24 * time.Hour
	callPruneEvery = time.Hour
	callPruneStamp = ".pruned"
)

// PruneCalls removes call logs idle for a day, unless it last ran within
// the hour. A log it cannot remove is left for the next run.
func (d Dir) PruneCalls(now time.Time) error {
	dir := filepath.Join(d.root(), callsDir)
	stamp := filepath.Join(dir, callPruneStamp)
	if st, err := os.Stat(stamp); err == nil && now.Sub(st.ModTime()) < callPruneEvery {
		return nil
	} else if errors.Is(err, os.ErrNotExist) {
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	if err := os.WriteFile(stamp, nil, 0o600); err != nil {
		return err
	}
	if err := os.Chtimes(stamp, now, now); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < callLogIdle {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
	return nil
}
