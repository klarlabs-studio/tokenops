package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/internal/infra/readguard"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readGuardSource tags events ingested from the read guard's ledger.
const readGuardSource = "read-guard"

// runReadGuardIngest publishes the read guard's prevented re-reads as
// optimization events.
//
// The guard runs as a short-lived hook process inside the client, so it
// cannot reach the daemon's event bus directly — it appends to a ledger on
// disk instead. Without something reading that ledger back, hundreds of
// thousands of genuinely reclaimed tokens stayed invisible to the rest of
// the system, and TEU reported "not measured" while real uplift was
// happening. TEU counted only optimizer events from the proxy, so a client
// that never proxies could never score on it.
//
// Each reclamation carries a stable ID, and the store dedups on it, so
// re-scanning the whole ledger every tick republishes the same savings
// harmlessly rather than inflating the total.
func runReadGuardIngest(
	ctx context.Context,
	bus events.Bus,
	logger *slog.Logger,
	interval time.Duration,
) {
	if bus == nil {
		return
	}
	if interval <= 0 {
		interval = 2 * time.Minute
	}
	scan := func() {
		recs, err := readguard.Reclamations("")
		if err != nil {
			logger.Debug("read-guard ingest failed", "err", err)
			return
		}
		for _, r := range recs {
			bus.Publish(reclamationEnvelope(r))
		}
	}
	scan()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scan()
		}
	}
}

// reclamationEnvelope wraps one prevented re-read as an OptimizationEvent.
func reclamationEnvelope(r readguard.Reclamation) *eventschema.Envelope {
	id := r.ID()
	if id == "" {
		id = uuid.NewString()
	}
	return &eventschema.Envelope{
		ID:            id,
		SchemaVersion: eventschema.SchemaVersion,
		Type:          eventschema.EventTypeOptimization,
		Timestamp:     r.At,
		Source:        readGuardSource,
		Attributes: map[string]string{
			"path":       r.Path,
			"session_id": r.SessionID,
		},
		Payload: &eventschema.OptimizationEvent{
			Kind: eventschema.OptimizationTypeReadDedup,
			// Interactive: the guard did not recommend this, it applied
			// it — the re-read never reached the model.
			Mode:                   eventschema.OptimizationModeInteractive,
			Decision:               eventschema.OptimizationDecisionApplied,
			EstimatedSavingsTokens: r.Tokens,
			Reason:                 "read guard blocked a redundant unchanged re-read",
			// OptimizationEvent carries workflow/agent attribution, not
			// session; the session id rides in Attributes above.
			AgentID: r.AgentID,
		},
	}
}
