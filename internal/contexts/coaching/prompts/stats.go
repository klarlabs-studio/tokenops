package prompts

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// TurnStats are aggregate per-turn averages computed from assistant
// turns in the same JSONLs the prompt-coach extractor walks. The
// renderer multiplies these into Recommendation savings so the
// operator sees concrete tokens / dollars / minutes per win,
// not just an abstract "turns saved" count.
//
// Pricing assumptions are intentionally conservative defaults
// (claude-opus-4-7 list rates from the shared spend catalog) — the
// analyzer is provider-agnostic, so the CLI passes rates per
// invocation if it wants finer accuracy. For operator-level
// estimates the defaults are close enough.
type TurnStats struct {
	TotalTurns        int     `json:"total_turns"`
	AvgInputTokens    float64 `json:"avg_input_tokens"`
	AvgCachedTokens   float64 `json:"avg_cached_tokens"`
	AvgOutputTokens   float64 `json:"avg_output_tokens"`
	AvgCostUSD        float64 `json:"avg_cost_usd"`
	AvgSeconds        float64 `json:"avg_seconds"` // est. human attention per turn
	WindowDescription string  `json:"window_description,omitempty"`
}

// AssumedSecondsPerTurn is the human-attention cost we assign
// to each wasted turn. 45s is a compromise between fast acks
// (~5s) and re-issued directives that involve context re-load
// (~120s).
const AssumedSecondsPerTurn = 45.0

// pricingTable is the shared spend catalog turn costs are priced from.
var pricingTable = spend.DefaultTable()

// defaultTurnRate prices turns whose JSONL lines carry no model field
// (older Claude Code formats, Codex variants). claude-opus-4-7 is the
// model TokenOps most often sees in such files; resolved from the
// shared catalog so price updates land here automatically instead of
// drifting in duplicated constants.
var defaultTurnRate = func() spend.Rate {
	r, err := pricingTable.Lookup(eventschema.ProviderAnthropic, "claude-opus-4-7")
	if err != nil {
		// The catalog ships with the binary; a missing row is a build
		// defect surfaced by the spend package's catalog tests.
		panic(err)
	}
	return r
}()

// turnRateProviders is the lookup order for rateForModel. Model IDs are
// distinct across vendors, so first match wins.
var turnRateProviders = []eventschema.Provider{
	eventschema.ProviderAnthropic,
	eventschema.ProviderOpenAI,
	eventschema.ProviderGemini,
	eventschema.ProviderMistral,
}

// rateForModel resolves the catalog rate for the model observed on a
// JSONL turn. Unknown or missing models fall back to defaultTurnRate so
// savings projections stay populated rather than silently zeroing.
func rateForModel(model string) spend.Rate {
	if model == "" {
		return defaultTurnRate
	}
	for _, p := range turnRateProviders {
		if r, err := pricingTable.Lookup(p, model); err == nil {
			return r
		}
	}
	return defaultTurnRate
}

// turnLine is the minimum JSONL shape ComputeTurnStats reads. Only
// assistant turns with a non-zero usage block contribute. Model, when
// present, prices the turn at that model's catalog rate.
type turnLine struct {
	Type    string `json:"type"`
	Message struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ComputeTurnStats walks the same JSONL tree the prompt-coach
// extractor uses and aggregates assistant-turn token + cost
// averages. Filters mirror ExtractOptions.Since/Until so a
// 30-day call honors the same window.
func ComputeTurnStats(opts ExtractOptions) (TurnStats, error) {
	root := opts.Root
	if opts.Source == SourceAuto && root == "" {
		return computeTurnStatsAuto(opts)
	}
	home, _ := os.UserHomeDir()
	if root == "" {
		if opts.Source == SourceCodex {
			root = filepath.Join(home, ".codex", "sessions")
		} else {
			root = filepath.Join(home, ".claude", "projects")
		}
	}
	return computeTurnStatsRoot(root, opts)
}

// computeTurnStatsAuto unions Claude Code + Codex JSONL roots —
// matching the Extract default — so a single coach run reflects
// every CLI the operator uses.
func computeTurnStatsAuto(opts ExtractOptions) (TurnStats, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return TurnStats{}, err
	}
	var acc TurnStats
	for _, root := range []string{
		filepath.Join(home, ".claude", "projects"),
		filepath.Join(home, ".codex", "sessions"),
	} {
		if _, statErr := os.Stat(root); statErr != nil {
			continue
		}
		s, err := computeTurnStatsRoot(root, opts)
		if err != nil {
			return TurnStats{}, err
		}
		acc = mergeStats(acc, s)
	}
	return finalizeStats(acc, opts), nil
}

// computeTurnStatsRoot scans one tree and returns the per-turn
// rollup. Each line is parsed independently — malformed lines are
// skipped rather than aborting the walk.
func computeTurnStatsRoot(root string, opts ExtractOptions) (TurnStats, error) {
	var s TurnStats
	var totalIn, totalCached, totalOut int64
	// Per-model accumulators so each turn is priced at the rate of the
	// model that actually served it (mixed Opus/Sonnet/GPT sessions were
	// previously all priced as claude-opus-4-7).
	type modelTotals struct {
		in, cached, out int64
	}
	perModel := map[string]*modelTotals{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer func() { _ = f.Close() }()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), turnsScanBufSize)
		// File-level time filter check via mod time — coarse but
		// avoids scanning entirely out-of-window files. Per-line
		// timestamps would catch edge cases but the assistant turns
		// don't always carry them in Codex format.
		if info, statErr := d.Info(); statErr == nil {
			if !opts.Since.IsZero() && info.ModTime().Before(opts.Since) {
				return nil
			}
			if !opts.Until.IsZero() && info.ModTime().After(opts.Until) {
				return nil
			}
		}
		for scanner.Scan() {
			var t turnLine
			if jsonErr := json.Unmarshal(scanner.Bytes(), &t); jsonErr != nil {
				continue
			}
			if !strings.EqualFold(t.Type, "assistant") {
				continue
			}
			u := t.Message.Usage
			if u.InputTokens == 0 && u.OutputTokens == 0 &&
				u.CacheReadInputTokens == 0 && u.CacheCreationInputTokens == 0 {
				continue
			}
			s.TotalTurns++
			totalIn += u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
			totalCached += u.CacheReadInputTokens
			totalOut += u.OutputTokens
			mt, ok := perModel[t.Message.Model]
			if !ok {
				mt = &modelTotals{}
				perModel[t.Message.Model] = mt
			}
			mt.in += u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
			mt.cached += u.CacheReadInputTokens
			mt.out += u.OutputTokens
		}
		return nil
	})
	if err != nil {
		return TurnStats{}, err
	}
	if s.TotalTurns == 0 {
		return s, nil
	}
	s.AvgInputTokens = float64(totalIn) / float64(s.TotalTurns)
	s.AvgCachedTokens = float64(totalCached) / float64(s.TotalTurns)
	s.AvgOutputTokens = float64(totalOut) / float64(s.TotalTurns)
	var totalCost float64
	for model, mt := range perModel {
		rate := rateForModel(model)
		uncached := max(mt.in-mt.cached, 0)
		cachedRate := rate.CachedInputPerMillion
		if cachedRate == 0 {
			cachedRate = rate.InputPerMillion
		}
		totalCost += float64(uncached)*rate.InputPerMillion/1e6 +
			float64(mt.cached)*cachedRate/1e6 +
			float64(mt.out)*rate.OutputPerMillion/1e6
	}
	s.AvgCostUSD = totalCost / float64(s.TotalTurns)
	s.AvgSeconds = AssumedSecondsPerTurn
	return finalizeStats(s, opts), nil
}

// mergeStats accumulates two roll-ups (Claude Code + Codex). Avg
// values are recomputed by finalizeStats — the merge only sums.
func mergeStats(a, b TurnStats) TurnStats {
	tot := a.TotalTurns + b.TotalTurns
	if tot == 0 {
		return a
	}
	return TurnStats{
		TotalTurns:      tot,
		AvgInputTokens:  weightedAvg(a.AvgInputTokens, a.TotalTurns, b.AvgInputTokens, b.TotalTurns),
		AvgCachedTokens: weightedAvg(a.AvgCachedTokens, a.TotalTurns, b.AvgCachedTokens, b.TotalTurns),
		AvgOutputTokens: weightedAvg(a.AvgOutputTokens, a.TotalTurns, b.AvgOutputTokens, b.TotalTurns),
		AvgCostUSD:      weightedAvg(a.AvgCostUSD, a.TotalTurns, b.AvgCostUSD, b.TotalTurns),
	}
}

func weightedAvg(av float64, an int, bv float64, bn int) float64 {
	tot := an + bn
	if tot == 0 {
		return 0
	}
	return (av*float64(an) + bv*float64(bn)) / float64(tot)
}

// finalizeStats stamps the window label + assumed seconds-per-turn.
func finalizeStats(s TurnStats, opts ExtractOptions) TurnStats {
	if s.AvgSeconds == 0 {
		s.AvgSeconds = AssumedSecondsPerTurn
	}
	if !opts.Since.IsZero() {
		s.WindowDescription = opts.Since.Format(time.RFC3339)
	}
	return s
}

// SavingsEstimate is the tangible per-recommendation projection
// derived from TurnStats × Recommendation.EstimatedMonthlyTurnsSaved.
type SavingsEstimate struct {
	Turns      int     `json:"turns"`
	Tokens     int64   `json:"tokens"`
	CostUSD    float64 `json:"cost_usd"`
	Seconds    float64 `json:"seconds"`
	HoursSaved float64 `json:"hours_saved"`
}

// ProjectSavings computes the SavingsEstimate for one recommendation.
// Tokens = input + output (cache reads counted at the cache rate via
// AvgCostUSD already, so tokens here is the raw count for the
// "you'd skip this many tokens" line).
func ProjectSavings(rec Recommendation, stats TurnStats) SavingsEstimate {
	turns := rec.EstimatedMonthlyTurnsSaved
	tokens := int64(float64(turns) * (stats.AvgInputTokens + stats.AvgOutputTokens))
	cost := float64(turns) * stats.AvgCostUSD
	secs := float64(turns) * stats.AvgSeconds
	return SavingsEstimate{
		Turns:      turns,
		Tokens:     tokens,
		CostUSD:    cost,
		Seconds:    secs,
		HoursSaved: secs / 3600.0,
	}
}
