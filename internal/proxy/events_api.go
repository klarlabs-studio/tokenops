package proxy

import (
	"net/http"
)

// WithEventCounts installs the in-process domain-event counter accessor.
// When set, the daemon mounts GET /api/domain-events which surfaces the
// per-kind counters the audit + observ subsystems maintain.
func WithEventCounts(fn func() map[string]int64) Option {
	return func(s *Server) { s.eventCounts = fn }
}

// WithAuditDrops installs an accessor for audit-subscriber drop count.
// /api/domain-events includes the drop figure when set.
func WithAuditDrops(fn func() int64) Option {
	return func(s *Server) { s.auditDrops = fn }
}

func (s *Server) registerEventCountsRoute(mux *http.ServeMux) {
	if s.eventCounts == nil {
		return
	}
	mux.HandleFunc("GET /api/domain-events", func(w http.ResponseWriter, _ *http.Request) {
		counts := s.eventCounts()
		var total int64
		for _, v := range counts {
			total += v
		}
		payload := map[string]any{
			"counts": counts,
			"total":  total,
		}
		if s.auditDrops != nil {
			payload["audit_dropped"] = s.auditDrops()
		}
		writeAPIJSON(w, http.StatusOK, payload)
	})
}
