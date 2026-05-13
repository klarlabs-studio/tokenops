package mcp

import (
	"context"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
)

// DataWarning attaches to cost/headroom MCP responses when synthetic
// events dominate the queried window. Operators reading a high
// `synthetic_ratio_pct` know the headline numbers reflect leftover
// `tokenops demo` seeds rather than real activity.
type DataWarning struct {
	SyntheticRatioPct float64 `json:"synthetic_ratio_pct"`
	RealEventCount    int64   `json:"real_event_count"`
	DemoEventCount    int64   `json:"demo_event_count"`
	Hint              string  `json:"hint"`
}

// dataWarningThresholdPct is the synthetic-dominance ratio above which
// MCP responses attach a DataWarning. 10% chosen so a fully real
// install never sees the banner, while typical post-`tokenops demo`
// state (90%+ synthetic) trips it loudly. Tune after customer-
// discovery interviews surface what operators actually find useful.
const dataWarningThresholdPct = 10.0

// maybeDataWarning returns a DataWarning when synthetic events make up
// more than dataWarningThresholdPct of the window; nil otherwise. The
// window is [since, until]; pass time.Time{} for an open lower bound.
// Errors surface to the caller because they likely indicate storage
// problems the operator should see.
func maybeDataWarning(ctx context.Context, store *sqlite.Store, since, until time.Time) (*DataWarning, error) {
	if store == nil {
		return nil, nil
	}
	counts, err := store.CountBySource(ctx, since, until)
	if err != nil {
		return nil, err
	}
	var real, demo int64
	for source, n := range counts {
		switch source {
		case "demo":
			demo += n
		default:
			real += n
		}
	}
	total := real + demo
	if total == 0 || demo == 0 {
		return nil, nil
	}
	pct := float64(demo) / float64(total) * 100
	if pct <= dataWarningThresholdPct {
		return nil, nil
	}
	return &DataWarning{
		SyntheticRatioPct: roundPct(pct),
		RealEventCount:    real,
		DemoEventCount:    demo,
		Hint:              "Synthetic events dominate this window. Run `tokenops demo --reset` to clear them or pass `include_demo: true` to opt in.",
	}, nil
}

func roundPct(v float64) float64 {
	// One decimal place matches the rest of the cost surface.
	return float64(int(v*10+0.5)) / 10
}
