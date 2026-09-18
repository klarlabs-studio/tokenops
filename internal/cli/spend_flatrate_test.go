package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast"
)

func flatRateView() spendView {
	return spendView{
		Window:   "since=x",
		Currency: "USD",
		Summary: analytics.Summary{
			Requests: 10, TotalTokens: 5_000_000,
			CostUSD: 0, APIEquivalentUSD: 3655.26,
		},
		BurnTokens24h: 874_275_305,
		GroupBy:       "model",
		GroupRows: []analytics.Row{
			{GroupKey: "claude-opus-5", Requests: 9, CostUSD: 0, APIEquivalentUSD: 3446.57},
			{GroupKey: "claude-sonnet-5", Requests: 1, CostUSD: 0, APIEquivalentUSD: 203.40},
		},
		Forecast: []forecast.Prediction{
			{At: time.Now(), Value: 0, Lower: 0, Upper: 0},
			{At: time.Now(), Value: 0, Lower: 0, Upper: 0},
		},
		HideSparkline: true,
	}
}

// A "top consumers" table whose only money column is structurally $0 ranks
// nothing and tells the operator nothing.
func TestSpendTextShowsTheEquivalentWhenCostIsPlanCovered(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSpendText(&buf, flatRateView()); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "API EQUIV") {
		t.Errorf("want an API EQUIV column on a plan-covered window:\n%s", out)
	}
	if !strings.Contains(out, "3446.5700") {
		t.Errorf("want the per-model equivalent rendered:\n%s", out)
	}
}

// A metered deployment already has a meaningful COST column; a second column
// repeating it is noise.
func TestSpendTextOmitsTheEquivalentColumnWhenMetered(t *testing.T) {
	v := flatRateView()
	v.GroupRows = []analytics.Row{{GroupKey: "gpt-5.5", Requests: 3, CostUSD: 12.5, APIEquivalentUSD: 12.5}}
	var buf bytes.Buffer
	if err := writeSpendText(&buf, v); err != nil {
		t.Fatalf("write: %v", err)
	}
	if strings.Contains(buf.String(), "API EQUIV") {
		t.Errorf("metered rows should not get a duplicate column:\n%s", buf.String())
	}
}

// Seven rows of $0.0000 with confidence bands read as a broken forecaster
// rather than as the correct answer to a question that does not apply.
func TestSpendTextReplacesAnAllZeroUSDForecastWithAReason(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSpendText(&buf, flatRateView()); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Forecast (next") {
		t.Errorf("an all-zero USD forecast should not be tabulated:\n%s", out)
	}
	if !strings.Contains(out, "No USD forecast") {
		t.Errorf("want the reason stated rather than silence:\n%s", out)
	}
}

func TestSpendTextKeepsANonZeroUSDForecast(t *testing.T) {
	v := flatRateView()
	v.Forecast = []forecast.Prediction{{At: time.Now(), Value: 3.5, Lower: 1, Upper: 9}}
	var buf bytes.Buffer
	if err := writeSpendText(&buf, v); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(buf.String(), "Forecast (next") {
		t.Errorf("a real forecast must still render:\n%s", buf.String())
	}
}

// "0.0000 USD / 874275305 tokens" spends its first half saying nothing.
func TestSpendTextBurnRateDropsTheMeaninglessZero(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSpendText(&buf, flatRateView()); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "burn rate (24h): 0.0000") {
		t.Errorf("plan-covered burn should not lead with $0:\n%s", out)
	}
	if !strings.Contains(out, "874275305 tokens") {
		t.Errorf("want the tokens that are the actual burn:\n%s", out)
	}
}
