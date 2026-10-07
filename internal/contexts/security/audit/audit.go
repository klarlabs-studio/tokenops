// Package audit provides an append-only log of governance-relevant
// TokenOps actions: config changes, optimization accepts/rejects,
// telemetry toggles, redaction-rule edits, etc. The log is backed by a
// dedicated audit_log table in the local SQLite store and is read-only
// by design — there is no Update or Delete API. The table is reached
// through the Store port, which *sqlite.Store implements; this package
// does no I/O of its own.
//
// The intent is operational: when "we accidentally enabled cloud
// telemetry last Thursday" surfaces, the audit table is the system of
// record. It is not a security log; high-volume signals (every prompt
// event) live in the events table.
package audit

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Action enumerates the audit-able event types. New values append.
type Action string

// Known actions.
const (
	ActionConfigChange       Action = "config_change"
	ActionOptimizationAccept Action = "optimization_accept"
	ActionOptimizationReject Action = "optimization_reject"
	ActionTelemetryToggle    Action = "telemetry_toggle"
	ActionRedactionUpdate    Action = "redaction_update"
	ActionBudgetUpdate       Action = "budget_update"
	ActionBudgetExceeded     Action = "budget_exceeded"
	ActionOptimizationApply  Action = "optimization_apply"
	ActionDataExport         Action = "data_export"
	// ActionPlanChange records a plan switch, and how much recorded usage
	// a backdated one re-marked as plan-covered.
	ActionPlanChange Action = "plan_change"
	// ActionCostCorrection records usage TokenOps re-marked on its own to
	// correct how it had recorded it, with the reason.
	ActionCostCorrection Action = "cost_correction"
)

// Entry is one row of the audit log. ID is a UUIDv4 minted by Record;
// callers can pass an empty ID and Record will fill it in.
type Entry struct {
	ID        string
	Timestamp time.Time
	Action    Action
	Actor     string
	Target    string
	Details   map[string]any
}

// Store is the port the audit log is persisted through. *sqlite.Store
// implements it over the audit_log table. It is append-only: there is no
// update or delete.
type Store interface {
	// AppendAudit persists one entry as given; Recorder has already
	// validated it and filled its ID and Timestamp.
	AppendAudit(ctx context.Context, entry Entry) error
	// QueryAudit returns the entries matching f, newest first, at most
	// f.Limit of them. Recorder always passes a positive Limit.
	QueryAudit(ctx context.Context, f Filter) ([]Entry, error)
}

// Recorder writes entries to the audit store. Construct via
// NewRecorder; the zero value is unusable.
type Recorder struct {
	store Store
}

// NewRecorder returns a Recorder backed by store.
func NewRecorder(store Store) *Recorder { return &Recorder{store: store} }

// ErrNotInitialised reports a Recorder with no store to write to. A Store
// implementation returns it too when it is itself nil (a nil *sqlite.Store
// passed to NewRecorder), so the two read the same to a caller.
var ErrNotInitialised = errors.New("audit: recorder not initialised")

// ready reports whether r has a store to write to.
func (r *Recorder) ready() bool { return r != nil && r.store != nil }

// Record appends entry. ID/Timestamp are populated when zero. Returns
// the persisted Entry so callers can surface the canonical timestamp.
func (r *Recorder) Record(ctx context.Context, entry Entry) (Entry, error) {
	if !r.ready() {
		return Entry{}, ErrNotInitialised
	}
	if entry.Action == "" {
		return Entry{}, errors.New("audit: action required")
	}
	if entry.Actor == "" {
		return Entry{}, errors.New("audit: actor required")
	}
	if entry.ID == "" {
		entry.ID = uuid.NewString()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	if err := r.store.AppendAudit(ctx, entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// Filter narrows audit-log queries. Empty fields are unconstrained;
// Limit <= 0 falls back to defaultQueryLimit.
type Filter struct {
	Action Action
	Actor  string
	Since  time.Time
	Until  time.Time
	Limit  int
}

const defaultQueryLimit = 1000

// Query returns matching entries ordered by Timestamp descending (newest
// first — what dashboards and CLI tables want).
func (r *Recorder) Query(ctx context.Context, f Filter) ([]Entry, error) {
	if !r.ready() {
		return nil, ErrNotInitialised
	}
	if f.Limit <= 0 {
		f.Limit = defaultQueryLimit
	}
	return r.store.QueryAudit(ctx, f)
}
