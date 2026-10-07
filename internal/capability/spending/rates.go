package spending

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
)

// RateQuery filters the rate card.
type RateQuery struct {
	// Provider keeps one provider's models.
	Provider string
	// Model keeps models whose name contains it.
	Model string
	// Limit caps the rows; twenty when not positive.
	Limit int
}

// Rate is one model's per-million-token price.
type Rate struct {
	Model       string  `json:"model"`
	InputPerM   float64 `json:"input_per_1m_usd"`
	OutputPerM  float64 `json:"output_per_1m_usd"`
	CacheReadPM float64 `json:"cache_read_per_1m_usd,omitempty"`
}

// Rates is the rate card TokenOps costs with, and when it was fetched.
type Rates struct {
	Source    string `json:"source"`
	FetchedAt string `json:"fetched_at"`
	Models    int    `json:"models_in_snapshot"`
	Rates     []Rate `json:"rates"`
	Note      string `json:"note,omitempty"`
}

// RateCard reads the newest pricing snapshot in dir, or the built-in
// baseline, filtered by q.
func RateCard(dir string, q RateQuery) Rates {
	snap, ok := pricing.LatestSnapshot(dir)
	if !ok {
		snap = pricing.BaselineSnapshot()
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	out := Rates{Source: snap.Source, FetchedAt: snap.FetchedAt.UTC().Format(time.RFC3339), Models: len(snap.Rates)}
	keys := make([]string, 0, len(snap.Rates))
	for k := range snap.Rates {
		if q.Provider != "" && !strings.HasPrefix(k, strings.ToLower(q.Provider)+"/") {
			continue
		}
		if q.Model != "" && !strings.Contains(k, q.Model) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		out.Note = fmt.Sprintf("%d models matched; showing %d. Narrow with provider or model.", len(keys), limit)
		keys = keys[:limit]
	}
	for _, k := range keys {
		r := snap.Rates[k]
		out.Rates = append(out.Rates, Rate{Model: k, InputPerM: r.InputPerMillion, OutputPerM: r.OutputPerMillion, CacheReadPM: r.CachedInputPerMillion})
	}
	if len(out.Rates) == 0 {
		out.Note = "no model matched that filter"
	}
	return out
}
