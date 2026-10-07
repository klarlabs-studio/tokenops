package checkup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/geminicli"
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/opencodedb"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Usage is one harness and model's tokens and their value at API prices.
type Usage struct {
	Harness string `json:"harness,omitempty"`
	Model   string `json:"model,omitempty"`
	Turns   int    `json:"turns"`
	// InputTokens includes CachedTokens.
	InputTokens  int64 `json:"input_tokens"`
	CachedTokens int64 `json:"cached_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	// CostUSD is the value at list prices: what it would cost on an API
	// key, which on a flat-rate plan is a measure, not a bill.
	CostUSD float64 `json:"cost_usd"`
	// UnpricedTurns ran on a model the rate card does not know; their
	// tokens are counted and their cost is not guessed.
	UnpricedTurns int `json:"unpriced_turns,omitempty"`
}

// turn is one assistant turn from any client, normalised.
type turn struct {
	harness, model, session string
	provider                eventschema.Provider
	at                      time.Time
	input, cached, output   int64
	// written and written1h are the cache writes inside input, and of
	// those the one-hour ones; each bills above the input rate.
	written, written1h int64
}

// tally sums turns per harness and model and prices them.
type tally struct {
	engine *spend.Engine
	by     map[[2]string]*Usage
}

func (t *tally) add(tn turn) {
	key := [2]string{tn.harness, tn.model}
	u := t.by[key]
	if u == nil {
		u = &Usage{Harness: tn.harness, Model: tn.model}
		t.by[key] = u
	}
	u.Turns++
	u.InputTokens += tn.input
	u.CachedTokens += tn.cached
	u.OutputTokens += tn.output
	cost, err := t.engine.ComputeAt(&eventschema.PromptEvent{
		Provider: tn.provider, RequestModel: tn.model,
		InputTokens: tn.input, CachedInputTokens: tn.cached, OutputTokens: tn.output,
		CacheWriteInputTokens: tn.written, CacheWrite1hInputTokens: tn.written1h,
	}, tn.at)
	if err != nil {
		u.UnpricedTurns++
		return
	}
	u.CostUSD += cost
}

// usageRead is what readUsage gathered.
type usageRead struct {
	usage      []Usage
	turnsByCWD map[string]int
	// warnings names each source that could not be read in full.
	warnings []string
	// partial means the read stopped before every source was read, so the
	// totals undercount.
	partial bool
}

// reader reads one client's turns since since into add.
type reader func(ctx context.Context, since time.Time, add func(turn)) error

// readUsage reads every client's turns since since, and how many turns
// ran in each working directory (for the standing-context finding).
func readUsage(ctx context.Context, home string, since time.Time) usageRead {
	engine, err := pricing.EffectiveEngine(filepath.Join(home, ".tokenops", "pricing"))
	if err != nil {
		// A fresh machine has no refreshed card; the embedded one prices.
		engine = spend.NewEngine(spend.DefaultTable())
	}
	t := &tally{engine: engine, by: map[[2]string]*Usage{}}
	sessionTurns := map[string]int{}
	add := func(tn turn) {
		if tn.at.Before(since) || ctx.Err() != nil {
			return
		}
		t.add(tn)
		sessionTurns[tn.session]++
	}
	var r usageRead
	for _, read := range []reader{readClaude, readCodex, readGemini, readOpencode} {
		if err := read(ctx, since, add); err != nil {
			r.warnings = append(r.warnings, err.Error())
		}
	}
	if err := ctx.Err(); err != nil {
		r.partial = true
		r.warnings = append(r.warnings, fmt.Sprintf("usage read stopped early (%v): totals are partial", err))
	}
	r.usage = make([]Usage, 0, len(t.by))
	for _, u := range t.by {
		r.usage = append(r.usage, *u)
	}
	sort.Slice(r.usage, func(i, j int) bool { return r.usage[i].CostUSD > r.usage[j].CostUSD })
	r.turnsByCWD = turnsPerDir(sessionTurns, since)
	return r
}

// turnsPerDir folds turns per session into turns per working directory.
func turnsPerDir(sessionTurns map[string]int, since time.Time) map[string]int {
	dirs := sessiondirs.Find(sessiondirs.Roots{}, since)
	out := map[string]int{}
	for id, n := range sessionTurns {
		if d, ok := dirs[id]; ok && d.CWD != "" {
			out[d.CWD] += n
		}
	}
	return out
}

// recentFiles keeps the files changed since since: older ones cannot hold
// a turn in the window, and reading them is most of the cost.
func recentFiles(files []string, since time.Time) []string {
	out := files[:0:0]
	for _, f := range files {
		if info, err := os.Stat(f); err == nil && !info.ModTime().Before(since) {
			out = append(out, f)
		}
	}
	return out
}

// sessionFiles lists a client's session files under root. A root that
// does not exist means the client is not in use and is not worth a
// warning; one that exists but cannot be read is, or its usage would read
// as none.
func sessionFiles(harness, root string, rootErr error, find func(string) ([]string, error)) ([]string, error) {
	if rootErr != nil {
		return nil, fmt.Errorf("%s: locate session records: %w", harness, rootErr)
	}
	d, err := os.Open(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: session records unreadable, usage not counted: %w", harness, err)
	}
	_ = d.Close()
	files, err := find(root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: list session records: %w", harness, err)
	}
	return files, nil
}

// readFiles reads each file changed since since and reports the ones that
// could not be read. A file removed between listing and reading is gone,
// not unreadable. It stops when ctx ends; readUsage reports that.
func readFiles(ctx context.Context, harness string, files []string, since time.Time, read func(string) error) error {
	var (
		failed int
		first  error
	)
	for _, f := range recentFiles(files, since) {
		if ctx.Err() != nil {
			break
		}
		if err := read(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			failed++
			if first == nil {
				first = err
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%s: %d session file(s) unreadable, usage undercounted: %w", harness, failed, first)
	}
	return nil
}

func readClaude(ctx context.Context, since time.Time, add func(turn)) error {
	root, err := claudecodejsonl.DefaultRoot()
	return readClaudeAt(ctx, root, err, since, add)
}

func readClaudeAt(ctx context.Context, root string, rootErr error, since time.Time, add func(turn)) error {
	files, err := sessionFiles("Claude Code", root, rootErr, claudecodejsonl.FindSessionFiles)
	if err != nil {
		return err
	}
	// Claude Code writes a message once per content block; the message
	// id is what makes it one turn.
	seen := map[string]bool{}
	return readFiles(ctx, "Claude Code", files, since, func(f string) error {
		return claudecodejsonl.ReadFile(f, func(t claudecodejsonl.Turn) error {
			if t.MessageID != "" {
				if seen[t.MessageID] {
					return nil
				}
				seen[t.MessageID] = true
			}
			add(turn{
				harness: "Claude Code", model: t.Model, session: t.SessionID, at: t.Timestamp,
				provider: eventschema.ProviderAnthropic,
				input:    t.InputTokens + t.CacheReadInputTokens + t.CacheCreationInputTokens,
				cached:   t.CacheReadInputTokens, output: t.OutputTokens,
				written: t.CacheCreationInputTokens, written1h: t.CacheCreation1hInputTokens,
			})
			return nil
		})
	})
}

func readCodex(ctx context.Context, since time.Time, add func(turn)) error {
	root, err := codexjsonl.DefaultRoot()
	files, err := sessionFiles("Codex", root, err, codexjsonl.FindSessionFiles)
	if err != nil {
		return err
	}
	return readFiles(ctx, "Codex", files, since, func(f string) error {
		return codexjsonl.ReadFile(f, func(t codexjsonl.Turn) error {
			add(turn{
				harness: "Codex", model: t.Model, session: t.SessionID, at: t.Timestamp,
				provider: eventschema.ProviderOpenAI,
				input:    t.InputTokens, cached: t.CachedTokens, output: t.OutputTokens + t.ReasoningTok,
			})
			return nil
		})
	})
}

func readGemini(ctx context.Context, since time.Time, add func(turn)) error {
	root, err := geminicli.DefaultRoot()
	files, err := sessionFiles("Gemini CLI", root, err, geminicli.FindSessionFiles)
	if err != nil {
		return err
	}
	now := time.Now()
	return readFiles(ctx, "Gemini CLI", files, since, func(f string) error {
		return geminicli.ReadFile(f, now, func(t geminicli.Turn) error {
			add(turn{
				harness: "Gemini CLI", model: t.Model, session: t.SessionID, at: t.Timestamp,
				provider: eventschema.ProviderGemini,
				input:    int64(t.InputTokens), cached: int64(t.CachedTokens), output: int64(t.OutputTokens),
			})
			return nil
		})
	})
}

func readOpencode(_ context.Context, since time.Time, add func(turn)) error {
	path, err := opencodedb.DefaultPath()
	if err != nil {
		return fmt.Errorf("opencode: locate database: %w", err)
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("opencode: database unreadable, usage not counted: %w", err)
	}
	return opencodedb.Read(path, opencodedb.Options{Since: since}, func(m opencodedb.Message) error {
		if m.Role != opencodedb.Assistant {
			return nil
		}
		tk := m.Tokens
		add(turn{
			harness: "opencode", model: m.ModelID, session: m.SessionID, at: m.Created,
			provider: eventschema.Provider(m.ProviderID),
			input:    tk.Input + tk.CacheRead + tk.CacheWrite, cached: tk.CacheRead, output: tk.Output + tk.Reasoning,
			written: tk.CacheWrite,
		})
		return nil
	})
}

func total(us []Usage) Usage {
	var t Usage
	for _, u := range us {
		t.Turns += u.Turns
		t.InputTokens += u.InputTokens
		t.CachedTokens += u.CachedTokens
		t.OutputTokens += u.OutputTokens
		t.CostUSD += u.CostUSD
		t.UnpricedTurns += u.UnpricedTurns
	}
	return t
}
