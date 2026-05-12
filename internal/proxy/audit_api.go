package proxy

import (
	"net/http"
	"strconv"
	"time"

	"github.com/felixgeelhaar/tokenops/internal/contexts/observability/analytics"
	"github.com/felixgeelhaar/tokenops/internal/contexts/security/audit"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
)

// AuditHandlers exposes a read-only audit query surface. Daemons wire
// it once the store opens; the CLI's `tokenops audit` and the MCP
// `tokenops_audit` tool already query the same store directly, so
// /api/audit completes the parity triangle.
type AuditHandlers struct {
	rec *audit.Recorder
}

// NewAuditHandlers wraps an audit.Recorder for the HTTP layer.
func NewAuditHandlers(store *sqlite.Store) *AuditHandlers {
	if store == nil {
		return nil
	}
	return &AuditHandlers{rec: audit.NewRecorder(store)}
}

// WithAudit installs the audit handler on the proxy.
func WithAudit(h *AuditHandlers) Option {
	return func(s *Server) { s.auditAPI = h }
}

// Register mounts GET /api/audit on mux.
func (h *AuditHandlers) Register(mux *http.ServeMux) {
	if h == nil {
		return
	}
	mux.HandleFunc("GET /api/audit", h.list)
}

func (h *AuditHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params := analytics.QueryParams{
		Since:        q.Get("since"),
		Until:        q.Get("until"),
		DefaultSince: 24 * time.Hour,
	}
	f, err := params.ToFilter()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	filter := audit.Filter{
		Action: audit.Action(q.Get("action")),
		Actor:  q.Get("actor"),
		Since:  f.Since,
		Until:  f.Until,
		Limit:  limit,
	}
	entries, err := h.rec.Query(r.Context(), filter)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
