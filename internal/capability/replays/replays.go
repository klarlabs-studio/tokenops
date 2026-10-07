// Package replays runs recorded prompts back through the optimizer
// pipeline, with the configured router, and reports what each step would
// have saved. `tokenops replay` asks it; it is a tool for working on
// TokenOps itself.
package replays

import (
	"context"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/replay"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// Selector picks the recorded prompts to replay: a session, a workflow or
// an agent, within an optional window.
type Selector = replay.SessionSelector

// Result is a replay: each step's prompt, what the pipeline recommended
// and what it would have saved.
type Result = replay.Result

// StepDiff is one replayed step.
type StepDiff = replay.StepDiff

// ErrEmptySession is returned when no recorded prompt matches.
var ErrEmptySession = replay.ErrEmptySession

// Run replays sel's prompts from store through the replay pipeline, with
// the router cfg configures, pricing with eng.
func Run(ctx context.Context, store *sqlite.Store, cfg config.Config, eng *spend.Engine, sel Selector) (*Result, error) {
	pipeline := replay.BuildPipeline(nil, replay.PipelineConfig{Routing: cfg.RouterConfig(), Spend: eng})
	return replay.New(store, pipeline, eng).Replay(ctx, sel)
}
