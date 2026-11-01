package prefixlint

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// Mid-session changes. A change to what a session started with — its tool
// or MCP server set, or an always-loaded file — would invalidate the
// cached prefix if the harness rewrote the prefix to match. Claude Code
// records such a change as an attachment it appends after the turns
// before (deferred_tools_delta, mcp_instructions_delta, edited_text_file),
// which leaves their cache alone, so a change is a finding only when the
// turn after it measurably rebuilt the prefix and nothing else the record
// shows explains it (waste.ClassifyRebuild: an idle gap past the cache's
// lifetime, a model switch, a compaction). Every change is still measured:
// the cache-read share of the turn just before it against the turn just
// after. Claude Code does not record the tool definitions it sends, so a
// change it made to them without an attachment is not seen.

// ChangeImpact is how one kind of mid-session change went for the prompt
// cache, over the transcripts read.
type ChangeImpact struct {
	// Kind is tool-set-change or context-edit.
	Kind string `json:"kind"`
	// Changes counts the changes made between two turns of a session; a
	// change before a session's first turn is how it starts, not a change.
	Changes  int `json:"changes"`
	Sessions int `json:"sessions"`
	// Rebuilds counts the changes whose next turn wrote again most of the
	// prefix the turn before had cached, with no idle gap, model switch
	// or compaction to explain it.
	Rebuilds int `json:"rebuilds"`
	// Explained counts the changes whose next turn rebuilt the cache for
	// a reason the record shows (an idle gap past the cache's lifetime, a
	// model switch, a compaction): a change made on resuming a session
	// an hour later meets a cold cache whatever it changed. They are left
	// out of the shares.
	Explained int `json:"explained"`
	// CacheReadShareBefore and CacheReadShareAfter are cache reads over
	// all input on the turns just before and just after the changes not
	// Explained.
	CacheReadShareBefore *float64 `json:"cache_read_share_before,omitempty"`
	CacheReadShareAfter  *float64 `json:"cache_read_share_after,omitempty"`
}

// contextFileNames are always-loaded files wherever they are.
var contextFileNames = map[string]bool{"CLAUDE.md": true, "CLAUDE.local.md": true, "AGENTS.md": true}

// editTools are Claude Code tools that change a file.
var editTools = map[string]bool{"Edit": true, "MultiEdit": true, "Write": true}

// changeTally adds up one kind of change.
type changeTally struct {
	changes, rebuilds, explained int
	sessions                     map[string]bool
	before, after                shareSum
	// files counts context edits and their rebuilds by file.
	files map[string]*fileTally
}

type shareSum struct{ input, read int64 }

type fileTally struct{ changes, rebuilds int }

// changeBook measures mid-session changes across transcripts.
type changeBook struct {
	kinds map[string]*changeTally
}

func newChangeBook() *changeBook { return &changeBook{kinds: map[string]*changeTally{}} }

// sessionTrack is one session's state while its transcript is read.
type sessionTrack struct {
	last   *waste.CacheTurn
	lastID string
	ttl    time.Duration
	// pending are the changes since the last turn: kind to the files
	// changed (empty for a tool set change).
	pending map[string]map[string]bool
	// loaded are the files the session's instructions listed.
	loaded map[string]bool
}

// transcriptLine is as much of a transcript line as the measure reads:
// token counts, times, models, tool names, file paths. Text is never kept.
type transcriptLine struct {
	Type        string    `json:"type"`
	SessionID   string    `json:"sessionId"`
	Timestamp   time.Time `json:"timestamp"`
	IsSidechain bool      `json:"isSidechain"`
	Message     *struct {
		ID      string          `json:"id"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			Input         int64 `json:"input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			CacheWrite    int64 `json:"cache_creation_input_tokens"`
			CacheCreation struct {
				Write1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
	Attachment *struct {
		Type     string `json:"type"`
		Filename string `json:"filename"`
		Files    []struct {
			Path string `json:"path"`
		} `json:"files"`
		AddedNames   []string `json:"addedNames"`
		RemovedNames []string `json:"removedNames"`
	} `json:"attachment"`
}

// line reads one transcript line into the sessions it belongs to.
func (b *changeBook) line(raw []byte, sessions map[string]*sessionTrack) {
	var l transcriptLine
	if json.Unmarshal(raw, &l) != nil || l.IsSidechain || l.SessionID == "" {
		return
	}
	s := sessions[l.SessionID]
	if s == nil {
		s = &sessionTrack{ttl: waste.CacheTTL, pending: map[string]map[string]bool{}, loaded: map[string]bool{}}
		sessions[l.SessionID] = s
	}
	switch {
	case l.Type == "assistant" && l.Message != nil && l.Message.Usage != nil:
		b.turn(l, s)
	case l.Type == "attachment" && l.Attachment != nil:
		a := l.Attachment
		switch a.Type {
		case "instructions":
			for _, f := range a.Files {
				s.loaded[filepath.Clean(f.Path)] = true
			}
		case "edited_text_file":
			s.contextEdit(a.Filename)
		case "deferred_tools_delta", "mcp_instructions_delta":
			if len(a.AddedNames)+len(a.RemovedNames) > 0 {
				s.change(KindToolSetChange, "")
			}
		}
	}
}

func (s *sessionTrack) change(kind, file string) {
	if s.pending[kind] == nil {
		s.pending[kind] = map[string]bool{}
	}
	if file != "" {
		s.pending[kind][file] = true
	}
}

func (s *sessionTrack) contextEdit(path string) {
	if path == "" {
		return
	}
	path = filepath.Clean(path)
	if s.loaded[path] || contextFileNames[filepath.Base(path)] {
		s.change(KindContextEdit, path)
	}
}

// turn closes the changes pending since the last turn against this one,
// and notes the files this turn's tool calls edit.
func (b *changeBook) turn(l transcriptLine, s *sessionTrack) {
	m := l.Message
	if m.ID == "" || m.ID != s.lastID {
		u := m.Usage
		cur := waste.CacheTurn{At: l.Timestamp, Model: m.Model, Input: u.Input + u.CacheRead + u.CacheWrite,
			CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, CacheWrite1h: u.CacheCreation.Write1h}
		if s.last != nil {
			for kind, files := range s.pending {
				b.record(kind, files, l.SessionID, *s.last, cur, s.ttl)
			}
		}
		s.pending = map[string]map[string]bool{}
		if d, wrote := waste.TTLOf(cur); wrote {
			s.ttl = d
		}
		s.last, s.lastID = &cur, m.ID
	}
	var blocks []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Input struct {
			FilePath string `json:"file_path"`
		} `json:"input"`
	}
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	for _, blk := range blocks {
		if blk.Type == "tool_use" && editTools[blk.Name] {
			s.contextEdit(blk.Input.FilePath)
		}
	}
}

func (b *changeBook) record(kind string, files map[string]bool, session string, prev, cur waste.CacheTurn, ttl time.Duration) {
	t := b.kinds[kind]
	if t == nil {
		t = &changeTally{sessions: map[string]bool{}, files: map[string]*fileTally{}}
		b.kinds[kind] = t
	}
	rb, ok := waste.ClassifyRebuild(prev, cur, ttl)
	rebuilt := ok && rb.Cause == waste.CausePrefixChanged
	t.changes++
	t.sessions[session] = true
	switch {
	case ok && rb.Cause != waste.CausePrefixChanged && rb.Cause != waste.CauseUnknown:
		t.explained++
	default:
		t.before.input += prev.Input
		t.before.read += prev.CacheRead
		t.after.input += cur.Input
		t.after.read += cur.CacheRead
	}
	if rebuilt {
		t.rebuilds++
	}
	for f := range files {
		ft := t.files[f]
		if ft == nil {
			ft = &fileTally{}
			t.files[f] = ft
		}
		ft.changes++
		if rebuilt {
			ft.rebuilds++
		}
	}
}

// impacts are the kinds measured, tool set changes first.
func (b *changeBook) impacts() []ChangeImpact {
	out := []ChangeImpact{}
	for _, kind := range []string{KindToolSetChange, KindContextEdit} {
		t := b.kinds[kind]
		if t == nil {
			continue
		}
		out = append(out, ChangeImpact{
			Kind: kind, Changes: t.changes, Sessions: len(t.sessions), Rebuilds: t.rebuilds, Explained: t.explained,
			CacheReadShareBefore: analytics.CacheReadShare(t.before.input, t.before.read),
			CacheReadShareAfter:  analytics.CacheReadShare(t.after.input, t.after.read),
		})
	}
	return out
}

// findings are the changes followed by a rebuild: one for the tool set,
// one per context file edited.
func (b *changeBook) findings() []Finding {
	var out []Finding
	base := Finding{Severity: SeverityMedium, Source: SourceSession, Clients: []string{ClientClaudeCode}}
	if t := b.kinds[KindToolSetChange]; t != nil && t.rebuilds > 0 {
		f := base
		f.Kind = KindToolSetChange
		f.Token = fmt.Sprintf("%d of %d changes followed by a rebuild", t.rebuilds, t.changes)
		f.Why = "Transcripts show the tool or MCP server set changing between turns of a session, and the next turn writing again most of the prompt the turn before had cached, " +
			"with no idle gap, model switch or compaction to explain it. The tool definitions open the prompt, so a change there rebuilds the cache of everything after it."
		f.Fix = "Settle the MCP servers and tools before the work starts: connect, authenticate or disable servers at the start of a session, not in the middle of one."
		out = append(out, f)
	}
	if t := b.kinds[KindContextEdit]; t != nil {
		files := make([]string, 0, len(t.files))
		for p, ft := range t.files {
			if ft.rebuilds > 0 {
				files = append(files, p)
			}
		}
		sort.Strings(files)
		for _, p := range files {
			ft := t.files[p]
			f := base
			f.Kind, f.Path = KindContextEdit, p
			f.Token = fmt.Sprintf("%d of %d edits followed by a rebuild", ft.rebuilds, ft.changes)
			f.Why = "Transcripts show this always-loaded file edited during a session, and the next turn writing again most of the prompt the turn before had cached, " +
				"with no idle gap, model switch or compaction to explain it."
			f.Fix = "Edit it between sessions; keep what changes during a session in a file the agent reads when it needs it, not in an always-loaded one."
			out = append(out, f)
		}
	}
	return out
}
