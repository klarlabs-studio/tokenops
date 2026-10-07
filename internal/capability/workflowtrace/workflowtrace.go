// Package workflowtrace answers "what happened in this workflow, and what
// did it waste": the workflow's steps reconstructed from the event store,
// priced, with the waste detector's findings attached. The daemon's
// GET /api/workflows/{id}, the tokenops_workflow_trace and
// tokenops_review_work tools, and `tokenops replay --workflow-id` all
// answer from it (ADR 0010).
package workflowtrace

import (
	"context"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/contexts/workflows/workflow"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// WasteConfig tunes the waste detector; its zero value takes the
// detector's defaults. Aliased so the coaching domain stays its single
// definition.
type WasteConfig = waste.Config

// Trace is a reconstructed workflow, aliased so the workflows domain
// stays its single definition.
type Trace = workflow.Trace

// ErrNoTrace means the store holds no prompts for the workflow.
var ErrNoTrace = workflow.ErrNoTrace

// Detail is one workflow's trace and what it wasted.
type Detail struct {
	Currency string                       `json:"currency"`
	Findings []*eventschema.CoachingEvent `json:"findings"`
	Trace    *Trace                       `json:"trace"`
}

// Reader reconstructs workflows from a store.
type Reader struct {
	store *sqlite.Store
	spend *spend.Engine
	waste WasteConfig
}

// NewReader reads workflows from store, priced by spendEng.
func NewReader(store *sqlite.Store, spendEng *spend.Engine, cfg WasteConfig) *Reader {
	return &Reader{store: store, spend: spendEng, waste: cfg}
}

// Detail reconstructs workflow id. It returns an error wrapping
// ErrNoTrace when the store has nothing for it.
func (r *Reader) Detail(ctx context.Context, id string) (Detail, error) {
	return Find(ctx, r.store, r.spend, id, r.waste)
}

// Find reconstructs workflow id from store, priced by spendEng, and runs
// the waste detector configured by cfg over it. Callers whose waste
// configuration can change between calls (the MCP server follows edits
// to coaching.context_limits) pass the one in effect now. It returns an
// error wrapping ErrNoTrace when the store has nothing for the workflow;
// Currency is empty when spendEng is nil.
func Find(ctx context.Context, store *sqlite.Store, spendEng *spend.Engine, id string, cfg WasteConfig) (Detail, error) {
	trace, err := workflow.Reconstruct(ctx, store, spendEng, id)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{
		Findings: waste.New(cfg).Detect(trace),
		Trace:    trace,
	}
	if spendEng != nil {
		d.Currency = spendEng.Currency()
	}
	return d, nil
}
