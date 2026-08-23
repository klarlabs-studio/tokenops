package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

func hourRows(costs []float64, tokens []int64) []analytics.Row {
	rows := make([]analytics.Row, 0, len(costs))
	base := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	for i := range costs {
		rows = append(rows, analytics.Row{
			BucketStart: base.Add(time.Duration(i) * time.Hour),
			CostUSD:     costs[i],
			TotalTokens: tokens[i],
		})
	}
	return rows
}

// On a flat-rate plan every row's CostUSD is 0, so a cost-keyed
// sparkline flattens to the baseline bar regardless of real traffic.
// The token series is what actually varies.
func TestBurnSparklineUsesTokensWhenSpendIsZero(t *testing.T) {
	rows := hourRows(
		[]float64{0, 0, 0, 0},
		[]int64{10_000, 900_000, 50_000, 2_000_000},
	)
	line := sparklineFromRowsBy(rows, func(r analytics.Row) float64 { return float64(r.TotalTokens) })
	if line == "" {
		t.Fatal("expected a sparkline")
	}
	if strings.Count(line, "▁") == len([]rune(line)) {
		t.Errorf("token sparkline is flat despite varying volume: %q", line)
	}
}

// The spend report must show token burn, not only a dollar figure that
// is structurally zero for subscription users.
func TestSpendTextReportsTokenBurn(t *testing.T) {
	v := spendView{
		Window:   "since=x",
		Currency: "USD",
		Summary: analytics.Summary{
			Requests: 100, InputTokens: 900_000, OutputTokens: 100_000, TotalTokens: 1_000_000,
		},
		BurnRate24h:   0,
		BurnTokens24h: 2_960_000,
		BurnSeries:    hourRows([]float64{0, 0}, []int64{1_000_000, 1_960_000}),
	}
	var buf bytes.Buffer
	if err := writeSpendText(&buf, v); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "2960000 tokens") {
		t.Errorf("token burn rate missing from report:\n%s", out)
	}
}
