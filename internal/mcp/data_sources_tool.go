package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// DataSourcesDeps wires the data-sources tool to the event store.
// Reuses the same *sqlite.Store the analytics tools already share so
// the count reflects exactly what the rollups see.
type DataSourcesDeps struct {
	Store *sqlite.Store
}

type dataSourcesInput struct {
	Since string `json:"since,omitempty" jsonschema:"description=RFC3339 timestamp or duration like '24h'; default 30d"`
	Until string `json:"until,omitempty"`
}

// dataSourcesWindow is the resolved time window echoed back to the caller.
type dataSourcesWindow struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

// dataSourcesResult is the typed payload for tokenops_data_sources. On the
// happy path Counts + Window are populated; the disabled-storage path sets
// Error + Hint instead (Counts/Window omitted).
type dataSourcesResult struct {
	Counts map[string]int64   `json:"counts,omitempty"`
	Window *dataSourcesWindow `json:"window,omitempty"`
	Error  string             `json:"error,omitempty"`
	Hint   string             `json:"hint,omitempty"`
}

// RegisterDataSourcesTool mounts tokenops_data_sources on s. The tool
// returns event counts grouped by the source column so operators can
// see real vs synthetic ratios at a glance.
func RegisterDataSourcesTool(s *Server, d DataSourcesDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_data_sources").
		Description("Return event counts grouped by source (proxy, mcp-session, demo, otlp, ...). Operators inspect this to confirm headroom and spend math are running on real data instead of leftover `tokenops demo` seeds.").
		OutputSchema(dataSourcesResult{}).
		Handler(func(ctx context.Context, in dataSourcesInput) (*dataSourcesResult, error) {
			if d.Store == nil {
				return &dataSourcesResult{
					Error: "storage_disabled",
					Hint:  "run `tokenops init` then restart the daemon",
				}, nil
			}
			since, until, err := parseDataSourceWindow(in)
			if err != nil {
				return nil, err
			}
			counts, err := d.Store.CountBySource(ctx, since, until)
			if err != nil {
				return nil, err
			}
			return &dataSourcesResult{
				Counts: counts,
				Window: &dataSourcesWindow{
					Since: fmtTimeOrEmpty(since),
					Until: fmtTimeOrEmpty(until),
				},
			}, nil
		})
	return nil
}

func parseDataSourceWindow(in dataSourcesInput) (time.Time, time.Time, error) {
	var since, until time.Time
	if in.Since != "" {
		t, err := parseTimeOrDuration(in.Since)
		if err != nil {
			return since, until, err
		}
		since = t
	} else {
		since = time.Now().Add(-30 * 24 * time.Hour)
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return since, until, err
		}
		until = t
	}
	return since, until, nil
}

func fmtTimeOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
