package auditlog

import (
	"context"
	"log/slog"

	"go.klarlabs.de/tokenops/internal/contexts/security/audit"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// Subscriber keeps the audit log from the domain events on a bus.
type Subscriber = audit.Subscriber

// Follow records the domain events the audit log keeps (budget breaches,
// applied optimizations) from bus into store, as actor, until the
// subscriber is closed.
func Follow(bus events.Observable, store *sqlite.Store, logger *slog.Logger, actor string) *Subscriber {
	return audit.Subscribe(bus, audit.NewRecorder(store), logger, actor)
}

// RecordConfigChange audits a change to the configuration made by actor.
func RecordConfigChange(ctx context.Context, store *sqlite.Store, actor, target string, details map[string]any) error {
	_, err := audit.NewRecorder(store).Record(ctx, audit.Entry{
		Action: audit.ActionConfigChange, Actor: actor, Target: target, Details: details,
	})
	return err
}
