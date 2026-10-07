package daemon

import (
	"context"
	"log/slog"
	"time"

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
// Each reclamation is published once per process: the ingester remembers
// what it published, and at boot the bus it is given skips what the store
// already holds. Re-publishing the whole ledger every tick was harmless to
// the store, which deduplicates on the stable ID, but not to the OTLP
// exporter on the same bus, which sent every reclamation again each tick.
func runReadGuardIngest(
	ctx context.Context,
	bus events.Bus,
	logger *slog.Logger,
	interval time.Duration,
	ledgerDir string,
) {
	if bus == nil {
		return
	}
	if interval <= 0 {
		interval = 2 * time.Minute
	}
	ing := &readGuardIngester{bus: bus, dir: ledgerDir, logger: logger}
	ing.scan(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ing.scan(ctx)
		}
	}
}

// readGuardIngester publishes the ledger's reclamations it has not yet
// published.
type readGuardIngester struct {
	bus       events.Bus
	dir       string
	logger    *slog.Logger
	published map[string]bool
}

// scan reads the ledger and publishes what is new.
func (g *readGuardIngester) scan(ctx context.Context) {
	if g.published == nil {
		g.published = map[string]bool{}
	}
	recs, err := readguard.Reclamations(g.dir)
	if err != nil {
		if g.logger != nil {
			g.logger.Debug("read-guard ingest failed", "err", err)
		}
		return
	}
	for _, r := range recs {
		id := r.ID()
		if g.published[id] {
			continue
		}
		// PublishWait, not Publish: the first scan replays the whole
		// ledger, the burst most likely to fill the queue, and Publish
		// discards the overflow silently. A drop counter non-zero on
		// every boot (222 events on one machine, in 13 seconds) is one
		// an operator stops reading.
		if err := g.bus.PublishWait(ctx, reclamationEnvelope(r)); err != nil {
			return // shutting down, or the bus is closed; retried next scan
		}
		g.published[id] = true
	}
}

// reclamationEnvelope wraps one prevented re-read as an OptimizationEvent.
func reclamationEnvelope(r readguard.Reclamation) *eventschema.Envelope {
	return &eventschema.Envelope{
		ID:            r.ID(),
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
