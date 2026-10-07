package coachhook

import (
	"math"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// A turn's cache writes bill at the card's write rates, not as plain
// input: on claude-sonnet-5 ($2 input) a five-minute write is $2.50 and a
// one-hour write $4 per million.
func TestTurnCostPricesCacheWrites(t *testing.T) {
	u := &usage{CacheCreationInputTokens: 1_000_000}
	u.CacheCreation.Ephemeral1hInputTokens = 400_000
	got := turnCostUSD(spend.DefaultTable(), u, "claude-sonnet-5")
	if want := 0.6*2.5 + 0.4*4; math.Abs(got-want) > 1e-9 {
		t.Errorf("cost = %v, want %v", got, want)
	}
}
