package coachhook

import (
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// usage is the token-usage block Claude Code records on each turn's message.
type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	// CacheCreation splits the writes by lifetime.
	CacheCreation struct {
		Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// transcriptLine is the subset of a transcript jsonl record we care about: the
// top-level ISO8601 timestamp plus the assistant message's usage and model.
type transcriptLine struct {
	Timestamp string `json:"timestamp"`
	// Subtype and CompactMetadata mark a compaction boundary, which says
	// whether the operator compacted or the client did at its ceiling.
	Subtype         string `json:"subtype"`
	CompactMetadata struct {
		Trigger string `json:"trigger"`
	} `json:"compactMetadata"`
	Message struct {
		Model string `json:"model"`
		Usage *usage `json:"usage"`
	} `json:"message"`
}

// claudePriced and codexPriced report whether the catalog knows a model,
// which is the difference between a turn that was free and a turn nobody
// could put a number on.
func claudePriced(tbl spend.Table, model string) bool {
	_, err := tbl.Lookup(eventschema.ProviderAnthropic, model)
	return model != "" && err == nil
}

// turnCostUSD prices a single turn's full API-equivalent cost: input, output,
// cache-write (cache_creation) and cache-read tokens each at the model's
// per-million rate from the spend catalog. Cache reads and writes use their
// own rates, which fall back to the input rate when the catalog has none
// (see spend.Rate); one-hour writes bill above five-minute ones.
// An unpriceable model yields 0 — the turn still counts (marker advances) but
// adds nothing, so the $ figure never over-states what we can defend.
func turnCostUSD(tbl spend.Table, u *usage, model string) float64 {
	if u == nil {
		return 0
	}
	r, err := tbl.Lookup(eventschema.ProviderAnthropic, model)
	if err != nil {
		return 0
	}
	written1h := max(0, min(u.CacheCreation.Ephemeral1hInputTokens, u.CacheCreationInputTokens))
	return perMillion(u.InputTokens, r.InputPerMillion) +
		perMillion(u.OutputTokens, r.OutputPerMillion) +
		perMillion(u.CacheCreationInputTokens-written1h, r.CacheWriteRate()) +
		perMillion(written1h, r.CacheWrite1hRate()) +
		perMillion(u.CacheReadInputTokens, r.CacheReadRate())
}
