package commits

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// TurnReader reads each session's turns from the event store.
type TurnReader interface {
	SessionTurns(ctx context.Context, f analytics.Filter) ([]analytics.SessionTurn, error)
}

// TurnsIn adapts a turn reader to Deps.Turns: the turns within base's
// bounds and sources, from whatever since the report asks for. A zero
// base reads every turn since since. The CLI, the MCP tool and the
// daemon API all read turns through it.
func TurnsIn(r TurnReader, base analytics.Filter) func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
	return func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
		f := base
		f.Since = since
		return r.SessionTurns(ctx, f)
	}
}
