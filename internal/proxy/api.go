package proxy

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/coaching/waste"
	"github.com/felixgeelhaar/tokenops/internal/contexts/observability/analytics"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/forecast"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/internal/contexts/workflows/workflow"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// AnalyticsHandlers wires the daemon's read-only analytics surface
// (/api/...) onto a mux. The dashboard skeleton consumes these
// endpoints; the same handlers feed the CLI's --json outputs and the
// MCP server. Mount via Server.WithAnalytics so the proxy package can
// register them alongside the provider routes.
type AnalyticsHandlers struct {
	store      *sqlite.Store
	aggregator *analytics.Aggregator
	spend      *spend.Engine
}

// NewAnalyticsHandlers builds the handlers. All deps are required.
func NewAnalyticsHandlers(store *sqlite.Store, agg *analytics.Aggregator, spendEng *spend.Engine) (*AnalyticsHandlers, error) {
	if store == nil || agg == nil || spendEng == nil {
		return nil, errors.New("proxy: AnalyticsHandlers requires store + aggregator + spend engine")
	}
	return &AnalyticsHandlers{store: store, aggregator: agg, spend: spendEng}, nil
}

// Register installs every endpoint on mux. Endpoints are read-only;
// callers should wrap them in dashauth.Middleware when authentication
// is required.
func (a *AnalyticsHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/spend/summary", a.spendSummary)
	mux.HandleFunc("GET /api/spend/series", a.spendSeries)
	mux.HandleFunc("GET /api/spend/forecast", a.spendForecast)
	mux.HandleFunc("GET /api/spend/cache_stats", a.spendCacheStats)
	mux.HandleFunc("GET /api/workflows", a.listWorkflows)
	mux.HandleFunc("GET /api/workflows/{id}", a.workflowDetail)
	mux.HandleFunc("GET /api/optimizations", a.listOptimizations)
}

// WithAnalytics installs analytics handlers on the proxy. Mounted
// under the same listener as the provider routes; the daemon decides
// whether to gate them behind dashauth.
func WithAnalytics(h *AnalyticsHandlers) Option {
	return func(s *Server) { s.analytics = h }
}

// --- helpers ------------------------------------------------------------

func filterFromQuery(r *http.Request, defaultSince time.Duration) (analytics.Filter, error) {
	q := r.URL.Query()
	return analytics.QueryParams{
		Since:        q.Get("since"),
		Until:        q.Get("until"),
		Provider:     q.Get("provider"),
		Model:        q.Get("model"),
		WorkflowID:   q.Get("workflow_id"),
		AgentID:      q.Get("agent_id"),
		DefaultSince: defaultSince,
	}.ToFilter()
}

func parseBucket(s string) analytics.Bucket {
	switch strings.ToLower(s) {
	case "day":
		return analytics.BucketDay
	default:
		return analytics.BucketHour
	}
}

func parseGroup(s string) analytics.Group {
	switch strings.ToLower(s) {
	case "provider":
		return analytics.GroupProvider
	case "workflow":
		return analytics.GroupWorkflow
	case "agent":
		return analytics.GroupAgent
	case "model":
		return analytics.GroupModel
	default:
		return analytics.GroupNone
	}
}

func writeAPIJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeAPIError(w http.ResponseWriter, status int, err error) {
	writeAPIJSON(w, status, map[string]string{"error": err.Error()})
}

// --- handlers -----------------------------------------------------------

func (a *AnalyticsHandlers) spendSummary(w http.ResponseWriter, r *http.Request) {
	filter, err := filterFromQuery(r, 7*24*time.Hour)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	summary, err := a.aggregator.Summarize(r.Context(), filter)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, map[string]any{
		"window":   filter,
		"summary":  summary,
		"currency": a.spend.Currency(),
	})
}

func (a *AnalyticsHandlers) spendSeries(w http.ResponseWriter, r *http.Request) {
	filter, err := filterFromQuery(r, 24*time.Hour)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	bucket := parseBucket(r.URL.Query().Get("bucket"))
	group := parseGroup(r.URL.Query().Get("group"))
	rows, err := a.aggregator.AggregateBy(r.Context(), filter, bucket, group)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, map[string]any{
		"bucket":   bucket,
		"group":    group,
		"rows":     rows,
		"currency": a.spend.Currency(),
	})
}

func (a *AnalyticsHandlers) spendForecast(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	horizon := 7
	if v := q.Get("horizon_days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 30 {
			horizon = n
		}
	}
	filter := analytics.Filter{Since: time.Now().Add(-30 * 24 * time.Hour)}
	rows, err := a.aggregator.AggregateBy(r.Context(), filter, analytics.BucketDay, analytics.GroupNone)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	history := forecast.SeriesFromRows(rows, forecast.CostUSD)
	preds := forecast.AutoForecast(history, horizon, 24*time.Hour)
	writeAPIJSON(w, http.StatusOK, map[string]any{
		"horizon_days":   horizon,
		"history_points": len(history),
		"history":        rows,
		"forecast":       preds,
		"currency":       a.spend.Currency(),
	})
}

// spendCacheStats reports the cache hit ratio across the events
// table. Cache reads bill at ~10% of new-input rate for Claude
// models — for agent-heavy workloads the ratio is the key efficiency
// number, often >95%. JSONL events carry the split in attributes
// (legacy) or payload.cached_input_tokens (post-v0.14.2); we
// COALESCE so both shapes work without a backfill.
func (a *AnalyticsHandlers) spendCacheStats(w http.ResponseWriter, r *http.Request) {
	filter, err := filterFromQuery(r, 24*time.Hour)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	stats, err := a.aggregator.CacheStats(r.Context(), filter)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, map[string]any{
		"window": filter,
		"stats":  stats,
	})
}

// listWorkflows returns one row per workflow_id with rolled-up metrics
// over the configured window. Used by the dashboard to populate the
// workflow drill-down table.
func (a *AnalyticsHandlers) listWorkflows(w http.ResponseWriter, r *http.Request) {
	filter, err := filterFromQuery(r, 7*24*time.Hour)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	rows, err := a.aggregator.AggregateBy(r.Context(), filter, analytics.BucketDay, analytics.GroupWorkflow)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	type entry struct {
		WorkflowID string  `json:"workflow_id"`
		Requests   int64   `json:"requests"`
		Tokens     int64   `json:"tokens"`
		CostUSD    float64 `json:"cost_usd"`
	}
	totals := map[string]*entry{}
	for _, row := range rows {
		key := row.GroupKey
		if key == "" {
			continue
		}
		cur, ok := totals[key]
		if !ok {
			cur = &entry{WorkflowID: key}
			totals[key] = cur
		}
		cur.Requests += row.Requests
		cur.Tokens += row.TotalTokens
		cur.CostUSD += row.CostUSD
	}
	out := make([]entry, 0, len(totals))
	for _, e := range totals {
		out = append(out, *e)
	}
	writeAPIJSON(w, http.StatusOK, map[string]any{
		"workflows": out,
		"currency":  a.spend.Currency(),
	})
}

func (a *AnalyticsHandlers) workflowDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeAPIError(w, http.StatusBadRequest, errors.New("workflow id required"))
		return
	}
	trace, err := workflow.Reconstruct(r.Context(), a.store, a.spend, id)
	if err != nil {
		if errors.Is(err, workflow.ErrNoTrace) {
			writeAPIError(w, http.StatusNotFound, err)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	findings := waste.New(waste.Config{}).Detect(trace)
	writeAPIJSON(w, http.StatusOK, map[string]any{
		"trace":    trace,
		"findings": findings,
		"currency": a.spend.Currency(),
	})
}

func (a *AnalyticsHandlers) listOptimizations(w http.ResponseWriter, r *http.Request) {
	filter, err := filterFromQuery(r, 7*24*time.Hour)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	envs, err := a.store.Query(r.Context(), sqlite.Filter{
		Type:       eventschema.EventTypeOptimization,
		WorkflowID: filter.WorkflowID,
		AgentID:    filter.AgentID,
		Since:      filter.Since,
		Until:      filter.Until,
		Limit:      500,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	type entry struct {
		Timestamp              time.Time `json:"timestamp"`
		Kind                   string    `json:"kind"`
		Mode                   string    `json:"mode"`
		Decision               string    `json:"decision"`
		EstimatedSavingsTokens int64     `json:"estimated_savings_tokens"`
		EstimatedSavingsUSD    float64   `json:"estimated_savings_usd"`
		QualityScore           float64   `json:"quality_score"`
		Reason                 string    `json:"reason"`
		WorkflowID             string    `json:"workflow_id,omitempty"`
		AgentID                string    `json:"agent_id,omitempty"`
	}
	out := make([]entry, 0, len(envs))
	for _, env := range envs {
		oe, ok := env.Payload.(*eventschema.OptimizationEvent)
		if !ok {
			continue
		}
		out = append(out, entry{
			Timestamp:              env.Timestamp,
			Kind:                   string(oe.Kind),
			Mode:                   string(oe.Mode),
			Decision:               string(oe.Decision),
			EstimatedSavingsTokens: oe.EstimatedSavingsTokens,
			EstimatedSavingsUSD:    oe.EstimatedSavingsUSD,
			QualityScore:           oe.QualityScore,
			Reason:                 oe.Reason,
			WorkflowID:             oe.WorkflowID,
			AgentID:                oe.AgentID,
		})
	}
	writeAPIJSON(w, http.StatusOK, map[string]any{
		"optimizations": out,
		"currency":      a.spend.Currency(),
	})
}
