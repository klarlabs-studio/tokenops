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

// TurnsFrom adapts a turn reader to Deps.Turns: every turn since since.
func TurnsFrom(r TurnReader) func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
	return func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
		return r.SessionTurns(ctx, analytics.Filter{Since: since})
	}
}
