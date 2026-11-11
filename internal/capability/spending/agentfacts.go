package spending

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/prompts"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/tools"
	"go.klarlabs.de/tokenops/internal/contexts/governance/scorecard"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/jsonltail"
	opencodestore "go.klarlabs.de/tokenops/internal/infra/opencodedb"
	"go.klarlabs.de/tokenops/internal/infra/scanscope"
)

// AgentFacts is what the agent KPIs are counted from, kept per session
// transcript, so that a scorecard reads only what was written since the
// last one asked.
//
// CGR and RGR need the prompts' text and TCS and DAR the tool blocks, and
// neither is in the event store. A scorecard used to read every transcript
// of its five weeks twice, once for each, and keep every prompt's text and
// every tool call to count them: gigabytes parsed on each request. The
// transcripts are append-only and the KPIs are counts, so one read keeps a
// few flags per prompt and per tool call, and the next read starts where
// this one stopped.
//
// It holds no prompt text and no command: what a prompt was is kept as
// whether it was an acknowledgement, a rejection or a loop sentinel. The
// zero value is ready to use, and it is safe for concurrent use.
type AgentFacts struct {
	mu sync.Mutex
	// tail is how far each transcript has been read.
	tail jsonltail.Tail[transcriptFacts]
	// files is what each transcript read so far holds, by path.
	files map[string]transcriptFacts
	// span is the furthest back a scorecard has asked. The transcripts
	// written within it stay read, so a week's scorecard after a month's
	// does not drop what the next month's would read again.
	span time.Duration
}

// transcriptFacts is one transcript's prompts and tool calls, as far as
// the agent KPIs count them.
type transcriptFacts struct {
	prompts []promptFact
	tools   []toolFact
}

// promptFact is one prompt the operator typed.
type promptFact struct {
	// at is when, in Unix nanoseconds.
	at              int64
	ack, regenerate bool
	// sentinel is the loop sentinel the prompt is and session the session
	// it was typed in; both are empty for any other prompt.
	sentinel, session string
}

// toolFact is one tool block the KPIs count.
type toolFact struct {
	at   int64
	kind toolKind
}

type toolKind uint8

const (
	// toolCall is a tool_use block.
	toolCall toolKind = iota
	// toolCallDestructive is a tool_use block running a destructive
	// command.
	toolCallDestructive
	// toolFailed is a tool_result block marked as an error.
	toolFailed
)

// toolCounts is one window's tool calls.
type toolCounts struct{ calls, destructive, failed int }

// transcriptKind is how one transcript is read.
type transcriptKind struct {
	source prompts.Source
	// scratch is a session run in a throwaway directory: its prompts are
	// a harness's, not the operator's.
	scratch bool
}

// loopSentinels are the prompts a /loop wakes the agent with.
var loopSentinels = map[string]string{"continue": "continue", "proceed": "proceed", "keep going": "keep going"}

// promptFactOf is what the KPIs keep of p.
func promptFactOf(p prompts.UserPrompt) promptFact {
	f := promptFact{at: p.Timestamp.UnixNano(), ack: prompts.IsAcknowledgement(p.Text), regenerate: prompts.IsRegenerate(p.Text)}
	if s, ok := loopSentinels[strings.ToLower(strings.TrimSpace(p.Text))]; ok {
		f.sentinel, f.session = s, strings.Clone(p.SessionID)
	}
	return f
}

// kpis are the agent KPIs for the period (from since) and for the
// operator's own baseline before it (from baselineSince), read from the
// transcripts under root, or from every harness's default location when
// root is empty. Either is a zero value, with no *Computed flag set, when
// it holds no prompts.
func (a *AgentFacts) kpis(ctx context.Context, root string, baselineSince, since, now time.Time) (period, baseline scorecard.AgentKPIInputs) {
	a.mu.Lock()
	defer a.mu.Unlock()
	roots, err := prompts.JSONLRoots(root)
	if err != nil {
		return period, baseline
	}
	a.span = max(a.span, now.Sub(baselineSince))
	kinds, err := transcriptsWrittenSince(roots, now.Add(-a.span))
	if err != nil {
		return period, baseline
	}
	a.read(ctx, kinds)

	from, split := baselineSince.UnixNano(), since.UnixNano()
	var (
		nowPrompts, basePrompts []promptFact
		nowTools, baseTools     toolCounts
	)
	prompt := func(f promptFact) {
		switch {
		case f.at < from:
		case f.at < split:
			basePrompts = append(basePrompts, f)
		default:
			nowPrompts = append(nowPrompts, f)
		}
	}
	for _, facts := range a.files {
		for _, f := range facts.prompts {
			prompt(f)
		}
		for _, f := range facts.tools {
			switch {
			case f.at < from:
			case f.at < split:
				baseTools.count(f.kind)
			default:
				nowTools.count(f.kind)
			}
		}
	}
	if root == "" {
		// opencode keeps its history in SQLite: a store to read, not a
		// file to follow. One that is missing is not an error; most
		// operators do not run it.
		typed, err := prompts.Extract(prompts.ExtractOptions{Source: prompts.SourceOpencode, Since: baselineSince, Opencode: opencodestore.Store{}})
		if err == nil {
			for _, p := range typed {
				prompt(promptFactOf(p))
			}
		}
	}
	return agentKPIsOf(nowPrompts, nowTools), agentKPIsOf(basePrompts, baseTools)
}

// read brings files up to date with the transcripts in kinds: what each
// gained since the last read, and nothing of one that did not change. A
// transcript no longer in kinds is forgotten.
func (a *AgentFacts) read(ctx context.Context, kinds map[string]transcriptKind) {
	if a.files == nil {
		a.files = map[string]transcriptFacts{}
	}
	paths := make([]string, 0, len(kinds))
	for path := range kinds {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	a.tail.Scan(ctx, paths, func(path string, r io.Reader, st *transcriptFacts) (int64, error) {
		n, err := readTranscriptFacts(path, kinds[path], r, st)
		// A file that was replaced is read into a fresh st, so this
		// replaces what the old one held.
		a.files[path] = *st
		return n, err
	}, func(string, error) {
		// A transcript that cannot be read now is read on the next ask.
	})
	for path := range a.files {
		if _, ok := kinds[path]; !ok {
			delete(a.files, path)
		}
	}
}

// readTranscriptFacts adds to st what the complete lines of r hold and
// returns the bytes they took. A last line still being written is left
// for the next read.
func readTranscriptFacts(path string, kind transcriptKind, r io.Reader, st *transcriptFacts) (int64, error) {
	var prompt func([]byte) (prompts.UserPrompt, bool)
	if !kind.scratch {
		prompt = prompts.LineParser(path, kind.source)
	}
	return jsonltail.ReadLines(r, false, func(line []byte) error {
		if prompt != nil {
			// A prompt with no time is in no window.
			if p, ok := prompt(line); ok && !p.Timestamp.IsZero() {
				st.prompts = append(st.prompts, promptFactOf(p))
			}
		}
		if kind.source != prompts.SourceClaudeCode {
			return nil
		}
		for _, ev := range tools.ParseLine(line) {
			switch {
			case ev.Destructive():
				st.tools = append(st.tools, toolFact{at: ev.Timestamp.UnixNano(), kind: toolCallDestructive})
			case !ev.IsResult:
				st.tools = append(st.tools, toolFact{at: ev.Timestamp.UnixNano(), kind: toolCall})
			case ev.IsError:
				st.tools = append(st.tools, toolFact{at: ev.Timestamp.UnixNano(), kind: toolFailed})
			}
		}
		return nil
	})
}

// transcriptsWrittenSince are the transcripts under roots last written at
// or after floor, and how each is read. Transcripts are append-only, so
// one written before floor holds no line after it.
func transcriptsWrittenSince(roots []prompts.TranscriptRoot, floor time.Time) (map[string]transcriptKind, error) {
	out := map[string]transcriptKind{}
	for _, root := range roots {
		err := filepath.WalkDir(root.Dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			if info, err := d.Info(); err == nil && info.ModTime().Before(floor) {
				return nil
			}
			out[path] = transcriptKind{source: root.Source, scratch: scanscope.EphemeralPath(path)}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (c *toolCounts) count(kind toolKind) {
	switch kind {
	case toolCall:
		c.calls++
	case toolCallDestructive:
		c.calls++
		c.destructive++
	case toolFailed:
		c.failed++
	}
}

// agentKPIsOf is the agent KPIs of one window's prompts and tool calls.
func agentKPIsOf(ps []promptFact, tc toolCounts) scorecard.AgentKPIInputs {
	ps = withoutLoopSentinels(ps)
	if len(ps) == 0 {
		return scorecard.AgentKPIInputs{}
	}
	var acks, regenerates int
	for _, p := range ps {
		if p.ack {
			acks++
		}
		if p.regenerate {
			regenerates++
		}
	}
	out := scorecard.AgentKPIInputs{
		ConfirmationGateRatePct:  100.0 * float64(acks) / float64(len(ps)),
		ConfirmationGateComputed: true,
		RegenerateRatePct:        100.0 * float64(regenerates) / float64(len(ps)),
		RegenerateComputed:       true,
	}
	if tc.calls > 0 {
		out.ToolSuccessRatePct = 100.0 * float64(tc.calls-tc.failed) / float64(tc.calls)
		out.ToolSuccessComputed = true
		out.DestructiveRatePct = 100.0 * float64(tc.destructive) / float64(tc.calls)
		out.DestructiveComputed = true
	}
	return out
}

// loopSentinelRepeats is the gap between "I typed continue twice" (real
// steering) and an autonomous /loop that fired dozens of times.
const loopSentinelRepeats = 5

// withoutLoopSentinels drops `continue` / `proceed` / `keep going`
// prompts that repeat more than loopSentinelRepeats times in one session.
// Those are /loop pacing sentinels (the harness waking the agent), not
// human acknowledgements, and counting them in CGR penalises autonomous
// workflows.
func withoutLoopSentinels(in []promptFact) []promptFact {
	type key struct{ session, sentinel string }
	counts := map[key]int{}
	for _, p := range in {
		if p.sentinel != "" {
			counts[key{p.session, p.sentinel}]++
		}
	}
	out := make([]promptFact, 0, len(in))
	for _, p := range in {
		if p.sentinel != "" && counts[key{p.session, p.sentinel}] > loopSentinelRepeats {
			continue
		}
		out = append(out, p)
	}
	return out
}
