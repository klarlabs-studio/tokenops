// Package workflowtrace answers "what happened in this workflow, and what
// did it waste": the workflow's steps reconstructed from the event store,
// priced, with the waste detector's findings attached. The daemon's
// GET /api/workflows/{id} serves it (ADR 0010).
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

// ErrNoTrace means the store holds no prompts for the workflow.
var ErrNoTrace = workflow.ErrNoTrace

// Detail is one workflow's trace and what it wasted.
type Detail struct {
	Currency string                       `json:"currency"`
	Findings []*eventschema.CoachingEvent `json:"findings"`
	Trace    *workflow.Trace              `json:"trace"`
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
	trace, err := workflow.Reconstruct(ctx, r.store, r.spend, id)
	if err != nil {
		return Detail{}, err
	}
	return Detail{
		Currency: r.spend.Currency(),
		Findings: waste.New(r.waste).Detect(trace),
		Trace:    trace,
	}, nil
}
