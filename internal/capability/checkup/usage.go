package checkup

import (
	"context"
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
	}, tn.at)
	if err != nil {
		u.UnpricedTurns++
		return
	}
	u.CostUSD += cost
}

// readUsage reads every client's turns since since, and how many turns
// ran in each working directory (for the standing-context finding).
func readUsage(ctx context.Context, home string, since time.Time) ([]Usage, map[string]int, []string) {
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
	var warnings []string
	for _, read := range []func(time.Time, func(turn)) error{readClaude, readCodex, readGemini, readOpencode} {
		if err := read(since, add); err != nil {
			warnings = append(warnings, err.Error())
		}
	}
	out := make([]Usage, 0, len(t.by))
	for _, u := range t.by {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CostUSD > out[j].CostUSD })
	return out, turnsPerDir(sessionTurns, since), warnings
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

func readClaude(since time.Time, add func(turn)) error {
	root, err := claudecodejsonl.DefaultRoot()
	if err != nil {
		return nil
	}
	files, err := claudecodejsonl.FindSessionFiles(root)
	if err != nil {
		return nil
	}
	// Claude Code writes a message once per content block; the message
	// id is what makes it one turn.
	seen := map[string]bool{}
	for _, f := range recentFiles(files, since) {
		_ = claudecodejsonl.ReadFile(f, func(t claudecodejsonl.Turn) error {
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
			})
			return nil
		})
	}
	return nil
}

func readCodex(since time.Time, add func(turn)) error {
	root, err := codexjsonl.DefaultRoot()
	if err != nil {
		return nil
	}
	files, err := codexjsonl.FindSessionFiles(root)
	if err != nil {
		return nil
	}
	for _, f := range recentFiles(files, since) {
		_ = codexjsonl.ReadFile(f, func(t codexjsonl.Turn) error {
			add(turn{
				harness: "Codex", model: t.Model, session: t.SessionID, at: t.Timestamp,
				provider: eventschema.ProviderOpenAI,
				input:    t.InputTokens, cached: t.CachedTokens, output: t.OutputTokens + t.ReasoningTok,
			})
			return nil
		})
	}
	return nil
}

func readGemini(since time.Time, add func(turn)) error {
	root, err := geminicli.DefaultRoot()
	if err != nil {
		return nil
	}
	files, err := geminicli.FindSessionFiles(root)
	if err != nil {
		return nil
	}
	now := time.Now()
	for _, f := range recentFiles(files, since) {
		_ = geminicli.ReadFile(f, now, func(t geminicli.Turn) error {
			add(turn{
				harness: "Gemini CLI", model: t.Model, session: t.SessionID, at: t.Timestamp,
				provider: eventschema.ProviderGemini,
				input:    int64(t.InputTokens), cached: int64(t.CachedTokens), output: int64(t.OutputTokens),
			})
			return nil
		})
	}
	return nil
}

func readOpencode(since time.Time, add func(turn)) error {
	path, err := opencodedb.DefaultPath()
	if err != nil {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
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
