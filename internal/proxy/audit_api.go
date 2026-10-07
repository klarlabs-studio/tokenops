package proxy

import (
	"net/http"

	"go.klarlabs.de/tokenops/internal/capability/auditquery"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// AuditHandlers exposes a read-only audit query surface. Daemons wire
// it once the store opens; the CLI's `tokenops audit` and the MCP
// `tokenops_records (view=audit)` tool already query the same store directly, so
// /api/audit completes the parity triangle.
type AuditHandlers struct {
	log *auditquery.Log
}

// NewAuditHandlers wraps the store's audit log for the HTTP layer.
func NewAuditHandlers(store *sqlite.Store) *AuditHandlers {
	if store == nil {
		return nil
	}
	return &AuditHandlers{log: auditquery.NewLog(store)}
}

// WithAudit installs the audit handler on the proxy.
func WithAudit(h *AuditHandlers) Option {
	return func(s *Server) { s.auditAPI = h }
}

// Register mounts GET /api/audit on mux.
func (h *AuditHandlers) Register(mux RouteMux) {
	if h == nil {
		return
	}
	mux.HandleFunc("GET /api/audit", h.list)
}

func (h *AuditHandlers) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query, err := auditquery.Parse(auditquery.Request{
		Since:  q.Get("since"),
		Until:  q.Get("until"),
		Action: q.Get("action"),
		Actor:  q.Get("actor"),
		Limit:  q.Get("limit"),
	})
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err)
		return
	}
	res, err := h.log.List(r.Context(), query)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err)
		return
	}
	writeAPIJSON(w, http.StatusOK, res)
}
