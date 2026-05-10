package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/felixgeelhaar/tokenops/internal/analytics"
	"github.com/felixgeelhaar/tokenops/internal/forecast"
	"github.com/felixgeelhaar/tokenops/internal/spend"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
)

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

			spendEng := spend.NewEngine(spend.DefaultTable())
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

			var predictions []forecast.Prediction
			if showForecast {
				horizon := forecastDays
				if horizon <= 0 {
					horizon = 7
				}
				history := forecast.SeriesFromRows(rows, forecast.CostUSD)
				if len(history) >= 4 {
					predictions, _ = forecast.NewHolt(0.6, 0.3).Forecast(history, horizon, 24*time.Hour)
				} else if len(history) >= 2 {
					predictions, _ = forecast.NewLinear().Forecast(history, horizon, 24*time.Hour)
				}
			}

			view := spendView{
				Window:        windowDescription(f),
				Currency:      spendEng.Currency(),
				Summary:       summary,
				GroupRows:     topRows(rows, topN),
				GroupBy:       string(group),
				BurnRate24h:   sumCost(burnRows),
				BurnSeries:    burnRows,
				Forecast:      predictions,
				HideSparkline: hideSparkline,
			}
			if jsonOut {
				return writeSpendJSON(cmd.OutOrStdout(), view)
			}
			return writeSpendText(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "path to events.db (defaults to config.storage.path)")
	cmd.Flags().StringVar(&groupBy, "by", "model", "group top consumers by: model | provider | workflow | agent")
	cmd.Flags().IntVar(&topN, "top", 5, "number of top consumers to print")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "lower bound (RFC3339 or duration; default 7d)")
	cmd.Flags().StringVar(&untilFlag, "until", "", "upper bound (RFC3339 timestamp)")
	cmd.Flags().BoolVar(&showForecast, "forecast", false, "include a spend forecast section")
	cmd.Flags().IntVar(&forecastDays, "forecast-days", 7, "forecast horizon in days")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of text")
	cmd.Flags().BoolVar(&hideSparkline, "no-sparkline", false, "suppress the burn sparkline")
	return cmd
}

// --- view + helpers -----------------------------------------------------

type spendView struct {
	Window        string                `json:"window"`
	Currency      string                `json:"currency"`
	Summary       analytics.Summary     `json:"summary"`
	GroupBy       string                `json:"group_by"`
	GroupRows     []analytics.Row       `json:"top"`
	BurnRate24h   float64               `json:"burn_rate_24h"`
	BurnSeries    []analytics.Row       `json:"burn_series"`
	Forecast      []forecast.Prediction `json:"forecast,omitempty"`
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
			continue
		}
		copy := rows[i]
		totals[key] = &copy
	}
	out := make([]analytics.Row, 0, len(totals))
	for _, r := range totals {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostUSD == out[j].CostUSD {
			return out[i].TotalTokens > out[j].TotalTokens
		}
		return out[i].CostUSD > out[j].CostUSD
	})
	if n > 0 && n < len(out) {
		out = out[:n]
	}
	return out
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
	if len(rows) == 0 {
		return ""
	}
	bars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	max := 0.0
	for _, r := range rows {
		if r.CostUSD > max {
			max = r.CostUSD
		}
	}
	if max == 0 {
		return strings.Repeat(string(bars[0]), len(rows))
	}
	out := make([]rune, len(rows))
	for i, r := range rows {
		idx := int(r.CostUSD / max * float64(len(bars)-1))
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
	fmt.Fprintf(w, "  burn rate (24h): %s", fmtMoney(v.BurnRate24h, v.Currency))
	if !v.HideSparkline {
		if line := sparklineFromRows(v.BurnSeries); line != "" {
			fmt.Fprintf(w, "  %s", line)
		}
	}
	fmt.Fprintln(w)

	if len(v.GroupRows) > 0 {
		fmt.Fprintf(w, "\nTop consumers by %s:\n", v.GroupBy)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "RANK\tKEY\tREQS\tIN TOK\tOUT TOK\tCOST")
		for i, r := range v.GroupRows {
			key := r.GroupKey
			if key == "" {
				key = "(unknown)"
			}
			fmt.Fprintf(tw, "%d\t%s\t%d\t%d\t%d\t%s\n",
				i+1, truncate(key, 32), r.Requests, r.InputTokens, r.OutputTokens,
				fmtMoney(r.CostUSD, v.Currency),
			)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}

	if len(v.Forecast) > 0 {
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
	return nil
}

func writeSpendJSON(w io.Writer, v spendView) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Suppress unused-import if context dropped during refactors.
var _ = context.Background
