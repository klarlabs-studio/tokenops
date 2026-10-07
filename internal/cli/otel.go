package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/capability/telemetry"
	"go.klarlabs.de/tokenops/internal/otlp"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// newOTelCmd shows what the daemon pushes to an OTLP collector: every
// metric with its attributes and value, read from this machine's data.
func newOTelCmd(rf *rootFlags) *cobra.Command {
	var (
		jsonOut bool
		dbPath  string
	)
	cmd := &cobra.Command{
		Use:   "otel",
		Short: "What the daemon pushes to an OpenTelemetry collector, figure by figure",
		Long: `otel prints the metrics the daemon pushes to the collector named in
otel.endpoint, computed now from this machine's data: plan window
utilization and pace, usage and its value, session grades, the coach's
findings and cost per commit. Figures only — no event, prompt, transcript,
file or commit subject is among them, and this is how to check.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(rf)
			if err != nil {
				return err
			}
			resolved, err := resolvePlanDB(dbPath)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			store, err := sqlite.OpenReadOnly(ctx, resolved)
			if err != nil {
				return fmt.Errorf("open store: %w (run `tokenops init`)", err)
			}
			defer func() { _ = store.Close() }()
			eng, err := buildSpendEngine(cfg)
			if err != nil {
				return err
			}
			deps := headroom.Deps{Config: &cfg, Reader: storeReader{store: store}, Price: eng.ComputeAt}
			g := &telemetry.Gatherer{
				Glance: func() headroom.Deps { return deps },
				Agg:    spending.NewAggregator(store, eng),
				Coach: func(now time.Time) *coachcap.Report {
					r := coachcap.Status(cfg, coachLedger(), contextLevers(), now)
					return &r
				},
			}
			gauges := telemetry.Gauges(g.Gather(ctx, time.Now().UTC()))
			if jsonOut {
				return writeControlJSON(cmd, gauges)
			}
			out := cmd.OutOrStdout()
			switch {
			case cfg.OTel.MetricsEnabled():
				every := cfg.OTel.Interval
				if every <= 0 {
					every = telemetry.Every
				}
				fmt.Fprintf(out, "Pushing every %s to %s\n\n", every, cfg.OTel.Endpoint)
			default:
				fmt.Fprintln(out, "Not pushing: set otel.enabled and otel.endpoint to send these to a collector.")
				fmt.Fprintln(out)
			}
			writeGauges(out, gauges)
			if cfg.OTel.EventsEnabled() {
				fmt.Fprintln(out, "\notel.events is on: every event is also forwarded as a log record (redacted when otel.redact is on).")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the gauges as JSON")
	cmd.Flags().StringVar(&dbPath, "db", "", "event store path (defaults to the configured one)")
	return cmd
}

func writeGauges(out io.Writer, gauges []otlp.Gauge) {
	if len(gauges) == 0 {
		fmt.Fprintln(out, "No figures yet: bind a plan and let the daemon read some usage.")
		return
	}
	for _, g := range gauges {
		unit := ""
		if g.Unit != "" && g.Unit != "1" {
			unit = " " + g.Unit
		}
		fmt.Fprintf(out, "%s\n", g.Name)
		points := append([]otlp.Point(nil), g.Points...)
		sort.Slice(points, func(i, j int) bool { return attrString(points[i].Attrs) < attrString(points[j].Attrs) })
		for _, p := range points {
			fmt.Fprintf(out, "  %-64s %14s%s\n", attrString(p.Attrs), trimFloat(p.Value), unit)
		}
	}
}

func attrString(a map[string]string) string {
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+a[k])
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func trimFloat(v float64) string {
	s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
	if s == "-0" {
		return "0"
	}
	return s
}
