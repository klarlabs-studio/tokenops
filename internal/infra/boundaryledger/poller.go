package boundaryledger

import (
	"context"
	"log/slog"
	"time"

	"go.klarlabs.de/tokenops/internal/events"
)

// Poller ingests the ledger into the event store, so boundary decisions
// join every other decision in explain and the decision history.
type Poller struct {
	bus      events.Bus
	dir      Dir
	interval time.Duration
	logger   *slog.Logger
	// sent are the envelope ids already handed to the bus. The store
	// drops a repeat anyway; this only saves re-sending the ledger every
	// tick.
	sent map[string]bool
}

// NewPoller reads dir (empty: the default) every interval (zero: one
// minute).
func NewPoller(bus events.Bus, dir Dir, interval time.Duration, logger *slog.Logger) *Poller {
	if interval <= 0 {
		interval = time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Poller{bus: bus, dir: dir, interval: interval, logger: logger, sent: map[string]bool{}}
}

// Run polls until ctx ends.
func (p *Poller) Run(ctx context.Context) error {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	p.Once(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			p.Once(ctx)
		}
	}
}

// Once ingests what the ledger holds that was not sent yet.
func (p *Poller) Once(ctx context.Context) {
	envs, err := p.dir.Read()
	if err != nil {
		p.logger.Warn("boundary ledger unreadable", "err", err)
		return
	}
	live := make(map[string]bool, len(envs))
	for _, env := range envs {
		live[env.ID] = true
		if p.sent[env.ID] || p.bus == nil {
			continue
		}
		if err := p.bus.PublishWait(ctx, env); err != nil {
			p.logger.Warn("boundary decision not stored", "err", err)
			return
		}
		p.sent[env.ID] = true
	}
	// Forget ids the ledger pruned, so the set stays the ledger's size.
	for id := range p.sent {
		if !live[id] {
			delete(p.sent, id)
		}
	}
}
