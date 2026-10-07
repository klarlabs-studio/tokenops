package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/state"

	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// DataSourcesDeps wires the data-sources tool to the event store.
// Reuses the same *sqlite.Store the analytics tools already share so
// the count reflects exactly what the rollups see.
type DataSourcesDeps struct {
	Store *sqlite.Store
	// Sources reads per-source ingestion health from the daemon. nil, or
	// a daemon that does not answer, leaves the result at counts alone —
	// which is what this tool returned before freshness existed.
	Sources func() (DaemonSources, error)
}

// freshness asks the daemon how each source is doing.
//
// The assessment is passed through rather than re-derived here. The
// daemon is the only process that holds all three facts — what was
// ingested, what each origin holds, and whether each reader is still
// succeeding — and two surfaces computing "healthy" separately is how
// they come to disagree in front of an operator.
//
// A daemon that is not running is the normal case for an MCP server
// started on its own, so its absence degrades the answer instead of
// failing it.
func (d DataSourcesDeps) freshness(context.Context) []state.SourceReport {
	if d.Sources == nil {
		return nil
	}
	got, err := d.Sources()
	if err != nil {
		return nil
	}
	return got.Sources
}

type dataSourcesInput struct {
	Since string `json:"since,omitempty" jsonschema:"description=RFC3339 timestamp or duration like '24h'; default 30d"`
	Until string `json:"until,omitempty"`
}

// dataSourcesWindow is the resolved time window echoed back to the caller.
// dataSourcesResult is the state capability's payload, shared with the
// daemon API (ADR 0010 §4).
type dataSourcesResult = state.DataSources

// RegisterDataSourcesTool mounts tokenops_status (view=data_sources) on s. The tool
// returns event counts grouped by the source column so operators can
// inspect local telemetry coverage at a glance.
func RegisterDataSourcesTool(s *Server, d DataSourcesDeps) error {
	if s == nil {
		return errors.New("mcp: server must not be nil")
	}
	s.Tool("tokenops_data_sources").
		Description("Return event counts grouped by source (proxy, mcp-session, otlp, ...) plus per-source ingestion health: when each source was last seen, when its reader last polled successfully, and what it last failed with. Operators inspect this to confirm headroom and spend math are running on observed data, and to tell a vendor they stopped using apart from a reader that has silently died.").
		OutputSchema(dataSourcesResult{}).
		Handler(func(ctx context.Context, in dataSourcesInput) (*dataSourcesResult, error) {
			since, until, err := parseDataSourceWindow(in)
			if err != nil {
				return nil, inputError(err)
			}
			var count state.Counter
			if d.Store != nil {
				count = d.Store.CountBySource
			}
			res, err := state.DataSourcesOf(ctx, count, d.freshness(ctx), since, until)
			if err != nil {
				return nil, err
			}
			return &res, nil
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
