package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/spend/forecast"
	"go.klarlabs.de/tokenops/internal/infra/svgchart"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// writeRatioSVG renders the input-vs-output token proportion — the ratio that
// makes the case that output-side compression is a rounding error. Output is
// drawn to scale (a sliver), which is the point.
func writeRatioSVG(path string, input, output int64) error {
	total := input + output
	if total == 0 {
		return fmt.Errorf("spend --svg: no tokens in window")
	}
	ratio := "—"
	if output > 0 {
		ratio = fmt.Sprintf("%d:1", input/output)
	}
	pct := func(v int64) string { return fmt.Sprintf("%.2f%%", 100*float64(v)/float64(total)) }
	bars := []svgchart.Bar{
		{Label: "Input (context re-sent every turn)", Display: pct(input), Frac: float64(input) / float64(total), Highlight: true},
		{Label: "Output (the model’s reply)", Display: pct(output), Frac: float64(output) / float64(total), Note: "drawn to scale — a hairline"},
	}
	svg := svgchart.HBars("Input vs. output tokens, on real usage — "+ratio, bars, svgchart.Options{
		Caption: "tokenops spend",
	})
	return os.WriteFile(path, []byte(svg), 0o644)
}

// newSpendCmd builds the `tokenops spend` subcommand. It surfaces three
// related views the operator typically wants alongside each other:
//
//   - headline summary (requests, tokens, cost) over the window;
//   - top consumers by group (model / provider / workflow / agent);
//   - burn rate (last 24h cost) and an optional 7-day forecast.
//
// The single command keeps the CLI footprint small. Sub-flags (--forecast,
// --burn, --top) decide which sections render.
func newSpendCmd(rf *rootFlags) *cobra.Command {
	var (
		dbPath        string
		groupBy       string
		topN          int
		sinceFlag     string
		untilFlag     string
		showForecast  bool
		forecastDays  int
		jsonOut       bool
		hideSparkline bool
		includeSrcs   []string
		svgFile       string
	)
	cmd := &cobra.Command{
		Use:   "spend",
		Short: "Show current spend, burn rate, and forecast",
		Long: `spend reads the local event store and prints a summary of the LLM
spend within the selected window. It surfaces:

  - headline tokens / cost
  - top consumers grouped by --by (model, provider, workflow, agent)
  - 24h burn rate, with an hourly sparkline
  - optional 7-day spend forecast (--forecast)`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			path, err := resolveStorageReadPath(dbPath, cfg.Storage.Path)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := sqlite.Open(ctx, path, sqlite.Options{})
			if err != nil {
				return fmt.Errorf("open event store: %w", err)
			}
			defer func() { _ = store.Close() }()

			group, err := parseGroup(groupBy)
			if err != nil {
				return err
			}
			f := analytics.Filter{}
			if sinceFlag != "" {
				since, err := parseSince(sinceFlag)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				f.Since = since
			} else {
				// Default window: last 7 days. Forecast still uses the
				// hourly bucket history regardless of this default.
				f.Since = time.Now().Add(-7 * 24 * time.Hour)
			}
			if untilFlag != "" {
				until, err := time.Parse(time.RFC3339, untilFlag)
				if err != nil {
					return fmt.Errorf("--until: %w", err)
				}
				f.Until = until
			}
			f.IncludeSources = resolveIncludeSources(cmd.ErrOrStderr(), includeSrcs)

			spendEng, err := buildSpendEngine(cfg)
			if err != nil {
				return err
			}
			agg := analytics.New(store, spendEng)
			summary, err := agg.Summarize(ctx, f)
			if err != nil {
				return err
			}
			rows, err := agg.AggregateBy(ctx, f, analytics.BucketDay, group)
			if err != nil {
				return err
			}

			// Burn-rate window: last 24h hourly.
			burnFilter := analytics.Filter{
				Since: time.Now().Add(-24 * time.Hour),
			}
			burnRows, err := agg.AggregateBy(ctx, burnFilter, analytics.BucketHour, analytics.GroupNone)
			if err != nil {
				return err
			}

			var predictions, tokenPredictions []forecast.Prediction
			if showForecast {
				horizon := forecastDays
				if horizon <= 0 {
					horizon = 7
				}
				history := forecast.SeriesFromRows(rows, forecast.CostUSD)
				predictions = forecast.AutoForecast(history, horizon, 24*time.Hour)
				tokenHistory := forecast.SeriesFromRows(rows, forecast.TotalTokens)
				tokenPredictions = forecast.AutoForecast(tokenHistory, horizon, 24*time.Hour)
			}

			view := spendView{
				Window:        windowDescription(f),
				Currency:      spendEng.Currency(),
				Summary:       summary,
				GroupRows:     topRows(rows, topN),
				GroupBy:       string(group),
				BurnRate24h:   sumCost(burnRows),
				BurnTokens24h: sumTokens(burnRows),
				BurnSeries:    burnRows,
				Forecast:      predictions,
				ForecastToks:  tokenPredictions,
				HideSparkline: hideSparkline,
			}
			if svgFile != "" {
				if err := writeRatioSVG(svgFile, summary.InputTokens, summary.OutputTokens); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s\n", svgFile)
			}
			if jsonOut {
				return writeSpendJSON(cmd.OutOrStdout(), view)
			}
			return writeSpendText(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().StringVar(&svgFile, "svg", "", "also write an input-vs-output ratio chart (ratio.svg) to this file")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to config.storage.path)")
	cmd.Flags().StringVar(&groupBy, "by", "model", "group top consumers by: model | provider | workflow | agent")
	cmd.Flags().IntVar(&topN, "top", 5, "number of top consumers to print")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "lower bound (RFC3339 or duration; default 7d)")
	cmd.Flags().StringVar(&untilFlag, "until", "", "upper bound (RFC3339 timestamp)")
	cmd.Flags().BoolVar(&showForecast, "forecast", false, "include a spend forecast section")
	cmd.Flags().IntVar(&forecastDays, "forecast-days", 7, "forecast horizon in days")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().BoolVar(&hideSparkline, "no-sparkline", false, "suppress the burn sparkline")
	cmd.Flags().StringSliceVar(&includeSrcs, "include-source", nil,
		"re-admit an excluded event source (repeatable, comma-separated): mcp-session")
	return cmd
}

// --- view + helpers -----------------------------------------------------

type spendView struct {
	Window      string            `json:"window"`
	Currency    string            `json:"currency"`
	Summary     analytics.Summary `json:"summary"`
	GroupBy     string            `json:"group_by"`
	GroupRows   []analytics.Row   `json:"top"`
	BurnRate24h float64           `json:"burn_rate_24h"`
	// BurnTokens24h is the same window measured in tokens. On a
	// subscription BurnRate24h is structurally zero, so this is the only
	// burn figure with signal.
	BurnTokens24h int64                 `json:"burn_tokens_24h"`
	BurnSeries    []analytics.Row       `json:"burn_series"`
	Forecast      []forecast.Prediction `json:"forecast,omitempty"`
	// ForecastToks projects the same horizon in tokens — the series that
	// stays meaningful when spend is plan-covered.
	ForecastToks  []forecast.Prediction `json:"forecast_tokens,omitempty"`
	HideSparkline bool                  `json:"-"`
}

func parseGroup(s string) (analytics.Group, error) {
	switch strings.ToLower(s) {
	case "", "model":
		return analytics.GroupModel, nil
	case "provider":
		return analytics.GroupProvider, nil
	case "workflow":
		return analytics.GroupWorkflow, nil
	case "agent":
		return analytics.GroupAgent, nil
	default:
		return "", fmt.Errorf("unknown --by value %q (use model|provider|workflow|agent)", s)
	}
}

func windowDescription(f analytics.Filter) string {
	parts := make([]string, 0, 2)
	if !f.Since.IsZero() {
		parts = append(parts, "since="+f.Since.Format(time.RFC3339))
	}
	if !f.Until.IsZero() {
		parts = append(parts, "until="+f.Until.Format(time.RFC3339))
	}
	if len(parts) == 0 {
		return "all time"
	}
	return strings.Join(parts, " ")
}

// topRows aggregates rows by group key (across buckets) and returns the
// top N by cost. AggregateBy emits one row per (bucket, key); summing
// across buckets gives the per-key total.
func topRows(rows []analytics.Row, n int) []analytics.Row {
	if len(rows) == 0 {
		return nil
	}
	totals := make(map[string]*analytics.Row)
	for i := range rows {
		key := rows[i].GroupKey
		if cur, ok := totals[key]; ok {
			cur.Requests += rows[i].Requests
			cur.InputTokens += rows[i].InputTokens
			cur.OutputTokens += rows[i].OutputTokens
			cur.TotalTokens += rows[i].TotalTokens
			cur.CostUSD += rows[i].CostUSD
			cur.APIEquivalentUSD += rows[i].APIEquivalentUSD
			continue
		}
		copy := rows[i]
		totals[key] = &copy
	}
	out := make([]analytics.Row, 0, len(totals))
	for _, r := range totals {
		out = append(out, *r)
	}
	// Ranked on the API-equivalent, not the real cost. On a flat-rate plan
	// every row's cost is $0 by design, so a cost-keyed ranking put every
	// consumer in a tie and left the order to the tiebreak — a "top
	// consumers" table that ranked nothing.
	sort.Slice(out, func(i, j int) bool {
		if out[i].APIEquivalentUSD == out[j].APIEquivalentUSD {
			return out[i].TotalTokens > out[j].TotalTokens
		}
		return out[i].APIEquivalentUSD > out[j].APIEquivalentUSD
	})
	if n > 0 && n < len(out) {
		out = out[:n]
	}
	return out
}

func sumTokens(rows []analytics.Row) int64 {
	var total int64
	for _, r := range rows {
		total += r.TotalTokens
	}
	return total
}

func sumCost(rows []analytics.Row) float64 {
	var total float64
	for _, r := range rows {
		total += r.CostUSD
	}
	return total
}

// sparklineFromRows renders a unicode block-bar sparkline scaled to the
// row series' max cost. Empty series renders an empty string.
func sparklineFromRows(rows []analytics.Row) string {
	return sparklineFromRowsBy(rows, func(r analytics.Row) float64 { return r.CostUSD })
}

// sparklineFromRowsBy renders rows through an arbitrary accessor. The
// cost accessor flattens to the baseline bar for plan-covered traffic —
// every row bills $0.00 — so callers plot tokens instead when that is
// the series carrying the variation.
func sparklineFromRowsBy(rows []analytics.Row, value func(analytics.Row) float64) string {
	if len(rows) == 0 {
		return ""
	}
	bars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	maxV := 0.0
	for _, r := range rows {
		if v := value(r); v > maxV {
			maxV = v
		}
	}
	if maxV == 0 {
		return strings.Repeat(string(bars[0]), len(rows))
	}
	out := make([]rune, len(rows))
	for i, r := range rows {
		idx := int(value(r) / maxV * float64(len(bars)-1))
		if idx >= len(bars) {
			idx = len(bars) - 1
		}
		if idx < 0 {
			idx = 0
		}
		out[i] = bars[idx]
	}
	return string(out)
}

// --- text rendering ----------------------------------------------------

func writeSpendText(w io.Writer, v spendView) error {
	fmt.Fprintf(w, "Spend report — %s\n", v.Window)
	fmt.Fprintf(w, "  requests:        %d\n", v.Summary.Requests)
	fmt.Fprintf(w, "  input tokens:    %d\n", v.Summary.InputTokens)
	fmt.Fprintf(w, "  output tokens:   %d\n", v.Summary.OutputTokens)
	fmt.Fprintf(w, "  total tokens:    %d\n", v.Summary.TotalTokens)
	fmt.Fprintf(w, "  total spend:     %s\n", fmtMoney(v.Summary.CostUSD, v.Currency))
	if v.Summary.APIEquivalentUSD > v.Summary.CostUSD {
		fmt.Fprintf(w, "  api equivalent:  %s (plan-covered usage at list price)\n",
			fmtMoney(v.Summary.APIEquivalentUSD, v.Currency))
	}
	// Plot whichever series actually varies: cost is a flat zero for
	// plan-covered traffic, so a cost-keyed sparkline would report calm
	// under any load.
	metric := func(r analytics.Row) float64 { return r.CostUSD }
	if v.BurnRate24h == 0 && v.BurnTokens24h > 0 {
		metric = func(r analytics.Row) float64 { return float64(r.TotalTokens) }
	}
	// Same again: "0.0000 USD / 874275305 tokens" spends its first half
	// saying nothing. On a plan-covered window the tokens are the burn.
	if v.BurnRate24h == 0 && v.BurnTokens24h > 0 {
		fmt.Fprintf(w, "  burn rate (24h): %d tokens (plan-covered, so $0 at the margin)", v.BurnTokens24h)
	} else {
		fmt.Fprintf(w, "  burn rate (24h): %s / %d tokens",
			fmtMoney(v.BurnRate24h, v.Currency), v.BurnTokens24h)
	}
	if !v.HideSparkline {
		if line := sparklineFromRowsBy(v.BurnSeries, metric); line != "" {
			fmt.Fprintf(w, "  %s", line)
		}
	}
	fmt.Fprintln(w)

	if len(v.Summary.Unpriced) > 0 {
		fmt.Fprintf(w, "\n⚠ no API rate for %d model(s) — API-equivalent is incomplete; metered cost may omit usage (plan-covered usage still costs $0 at the margin):\n", len(v.Summary.Unpriced))
		for _, u := range v.Summary.Unpriced {
			fmt.Fprintf(w, "    %s/%s (%d requests)\n", u.Provider, u.Model, u.Requests)
		}
		fmt.Fprintln(w, "  update pricing only when authoritative provider rates are available")
	}

	if len(v.GroupRows) > 0 {
		fmt.Fprintf(w, "\nTop consumers by %s:\n", v.GroupBy)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		// Same reasoning as the sparkline above: on a plan-covered window
		// COST is a column of zeros, so the figure that separates the rows
		// is the list-price equivalent. Shown only when it differs, so a
		// metered deployment does not get a duplicate column.
		showEquiv := groupRowsHaveEquivalent(v.GroupRows)
		header := "RANK\tKEY\tREQS\tIN TOK\tOUT TOK\tCOST"
		if showEquiv {
			header += "\tAPI EQUIV"
		}
		fmt.Fprintln(tw, header)
		for i, r := range v.GroupRows {
			key := r.GroupKey
			if key == "" {
				key = "(unknown)"
			}
			line := fmt.Sprintf("%d\t%s\t%d\t%d\t%d\t%s",
				i+1, truncate(key, 32), r.Requests, r.InputTokens, r.OutputTokens,
				fmtMoney(r.CostUSD, v.Currency),
			)
			if showEquiv {
				line += "\t" + fmtMoney(r.APIEquivalentUSD, v.Currency)
			}
			fmt.Fprintln(tw, line)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	if len(v.Forecast) > 0 && !allZeroForecast(v.Forecast) {
		fmt.Fprintf(w, "\nForecast (next %d points):\n", len(v.Forecast))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "WHEN\tEXPECTED\tLOW\tHIGH")
		for _, p := range v.Forecast {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
				p.At.Format("2006-01-02"),
				fmtMoney(p.Value, v.Currency),
				fmtMoney(p.Lower, v.Currency),
				fmtMoney(p.Upper, v.Currency),
			)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	if len(v.Forecast) > 0 && allZeroForecast(v.Forecast) {
		fmt.Fprintln(w, "\nNo USD forecast: this window is plan-covered, so every historical point is $0 "+
			"and a forecast of it would be seven rows of zero with confidence bands. The token forecast below is the real one.")
	}

	if len(v.ForecastToks) > 0 {
		fmt.Fprintf(w, "\nToken forecast (next %d points):\n", len(v.ForecastToks))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "WHEN\tEXPECTED\tLOW\tHIGH")
		for _, p := range v.ForecastToks {
			fmt.Fprintf(tw, "%s\t%.0f\t%.0f\t%.0f\n",
				p.At.Format("2006-01-02"), p.Value, p.Lower, p.Upper)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func writeSpendJSON(w io.Writer, v spendView) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Suppress unused-import if context dropped during refactors.
var _ = context.Background

// groupRowsHaveEquivalent reports whether the list-price equivalent says
// something the cost column does not — i.e. the window is plan-covered.
func groupRowsHaveEquivalent(rows []analytics.Row) bool {
	for _, r := range rows {
		if r.APIEquivalentUSD > r.CostUSD {
			return true
		}
	}
	return false
}

// allZeroForecast reports whether a projection is entirely zero.
//
// A flat-rate plan's cost history is zero at every point, so the forecaster
// dutifully projects zero forward and prints seven rows of $0.0000 with LOW
// and HIGH bands. That reads as a broken forecaster rather than as the
// correct answer to a question that does not apply, and it crowds out the
// token forecast, which is the one that carries information.
func allZeroForecast(points []forecast.Prediction) bool {
	for _, p := range points {
		if p.Value != 0 || p.Lower != 0 || p.Upper != 0 {
			return false
		}
	}
	return true
}
