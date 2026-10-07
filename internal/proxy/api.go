package proxy

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/spending"
	"go.klarlabs.de/tokenops/internal/capability/workflowtrace"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// AnalyticsHandlers wires the daemon's read-only analytics surface
// (/api/...) onto a mux. The dashboard skeleton consumes these
// endpoints; the same handlers feed the CLI's --json outputs and the
// MCP server. Mount via Server.WithAnalytics so the proxy package can
// register them alongside the provider routes.
type AnalyticsHandlers struct {
	store      *sqlite.Store
	aggregator *spending.EventAggregator
	spend      *spend.Engine
	workflows  *workflowtrace.Reader
}

// NewAnalyticsHandlers builds the handlers. Store, aggregator, and
// spend engine are required; wasteCfg's zero value uses the detector
// defaults.
func NewAnalyticsHandlers(store *sqlite.Store, agg *spending.EventAggregator, spendEng *spend.Engine, wasteCfg workflowtrace.WasteConfig) (*AnalyticsHandlers, error) {
	if store == nil || agg == nil || spendEng == nil {
		return nil, errors.New("proxy: AnalyticsHandlers requires store + aggregator + spend engine")
	}
	return &AnalyticsHandlers{
		store:      store,
		aggregator: agg,
		spend:      spendEng,
		workflows:  workflowtrace.NewReader(store, spendEng, wasteCfg),
	}, nil
}

// Register installs every endpoint on mux. Endpoints are read-only;
// callers should wrap them in BearerAuth (over dashauth) when authentication
// is required.
func (a *AnalyticsHandlers) Register(mux RouteMux) {
	mux.HandleFunc("GET /api/spend/summary", a.spendSummary)
	mux.HandleFunc("GET /api/spend/series", a.spendSeries)
	mux.HandleFunc("GET /api/spend/forecast", a.spendForecast)
	mux.HandleFunc("GET /api/spend/cache_stats", a.spendCacheStats)
	mux.HandleFunc("GET /api/workflows", a.listWorkflows)
	mux.HandleFunc("GET /api/workflows/{id}", a.workflowDetail)
	mux.HandleFunc("GET /api/optimizations", a.listOptimizations)
	mux.HandleFunc("GET /api/spend/top", a.spendTop)
	mux.HandleFunc("GET /api/spend/commits", a.spendCommits)
	mux.HandleFunc("GET /api/spend/burn-rate", a.spendBurnRate)
	mux.HandleFunc("GET /api/scorecard", a.scorecard)
	mux.HandleFunc("GET /api/decisions/{id}", a.decision)
}

// WithAnalytics installs analytics handlers on the proxy. Mounted
// under the same listener as the provider routes; the daemon decides
// whether to gate them behind dashauth.
func WithAnalytics(h *AnalyticsHandlers) Option {
	return func(s *Server) { s.analytics = h }
}

// --- helpers ------------------------------------------------------------

// windowFromQuery reads the window every rollup takes: ?since= (RFC 3339
// or a duration), ?until=, and the provider, model, workflow_id and
// agent_id narrowing.
func windowFromQuery(r *http.Request, defaultSince time.Duration) (spending.Window, error) {
	q := r.URL.Query()
	return spending.WindowOf(spending.WindowQuery{
		Since:      q.Get("since"),
		Until:      q.Get("until"),
		Provider:   q.Get("provider"),
		Model:      q.Get("model"),
		WorkflowID: q.Get("workflow_id"),
		AgentID:    q.Get("agent_id"),
	}, defaultSince)
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

// writeAPIResult renders a capability's answer: a 500 when it failed,
// the answer otherwise.
func writeAPIResult(w http.ResponseWriter, body any, err error) {
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, body)
}

// --- handlers -----------------------------------------------------------

func (a *AnalyticsHandlers) spendSummary(w http.ResponseWriter, r *http.Request) {
	win, err := windowFromQuery(r, 7*24*time.Hour)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	res, err := spending.Summary(r.Context(), a.aggregator, win, a.spend.Currency())
	writeAPIResult(w, res, err)
}

func (a *AnalyticsHandlers) spendSeries(w http.ResponseWriter, r *http.Request) {
	win, err := windowFromQuery(r, 24*time.Hour)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	q := r.URL.Query()
	res, err := spending.Series(r.Context(), a.aggregator, win, q.Get("bucket"), q.Get("group"), a.spend.Currency())
	writeAPIResult(w, res, err)
}

func (a *AnalyticsHandlers) spendForecast(w http.ResponseWriter, r *http.Request) {
	horizon := spending.HorizonDays(r.URL.Query().Get("horizon_days"))
	res, err := spending.Forecast(r.Context(), a.aggregator, horizon, a.spend.Currency(), time.Now())
	writeAPIResult(w, res, err)
}

// spendCacheStats reports the prompt-cache hit ratio over a window.
func (a *AnalyticsHandlers) spendCacheStats(w http.ResponseWriter, r *http.Request) {
	win, err := windowFromQuery(r, 24*time.Hour)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	res, err := spending.CacheStats(r.Context(), a.aggregator, win)
	writeAPIResult(w, res, err)
}

// listWorkflows returns one row per workflow_id with rolled-up metrics
// over the window.
func (a *AnalyticsHandlers) listWorkflows(w http.ResponseWriter, r *http.Request) {
	win, err := windowFromQuery(r, 7*24*time.Hour)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	res, err := spending.Workflows(r.Context(), a.aggregator, win, a.spend.Currency())
	writeAPIResult(w, res, err)
}

func (a *AnalyticsHandlers) workflowDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeAPIError(w, http.StatusBadRequest, errors.New("workflow id required"))
		return
	}
	detail, err := a.workflows.Detail(r.Context(), id)
	if errors.Is(err, workflowtrace.ErrNoTrace) {
		writeAPIError(w, http.StatusNotFound, err)
		return
	}
	writeAPIResult(w, detail, err)
}

func (a *AnalyticsHandlers) listOptimizations(w http.ResponseWriter, r *http.Request) {
	win, err := windowFromQuery(r, 7*24*time.Hour)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	res, err := spending.OptimizationsIn(r.Context(), a.store, win, a.spend.Currency())
	writeAPIResult(w, res, err)
}
