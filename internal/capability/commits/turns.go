package commits

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// TurnSource reads the session turns a filter selects.
type TurnSource interface {
	SessionTurns(ctx context.Context, f analytics.Filter) ([]analytics.SessionTurn, error)
}

// TurnsIn reads turns from src within base's bounds and sources, from
// whatever since the report asks for.
func TurnsIn(src TurnSource, base analytics.Filter) func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
	return func(ctx context.Context, since time.Time) ([]analytics.SessionTurn, error) {
		f := base
		f.Since = since
		return src.SessionTurns(ctx, f)
	}
}
