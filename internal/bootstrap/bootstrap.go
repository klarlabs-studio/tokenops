// Package bootstrap is the composition root for TokenOps.
//
// Every long-lived collaborator (sqlite.Store, spend.Engine, analytics
// aggregator, tokenizer registry, event bus, redaction pipeline) is
// constructed here so adapters (CLI, MCP, HTTP handlers, daemon) consume
// the same instances instead of each instantiating its own. This makes
// the DDD layering explicit: application services depend on ports the
// bootstrap wires to concrete infrastructure.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/contexts/observability/observ"
	"go.klarlabs.de/tokenops/internal/contexts/prompts/tokenizer"
	"go.klarlabs.de/tokenops/internal/contexts/security/redaction"
	"go.klarlabs.de/tokenops/internal/contexts/spend/pricing"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/domainevents"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// Components holds every long-lived collaborator the daemon needs. The
// zero value is not useful — always construct via New.
type Components struct {
	Store        *sqlite.Store
	Spend        *spend.Engine
	Aggregator   *analytics.Aggregator
	Tokenizers   *tokenizer.Registry
	Redactor     *redaction.Redactor
	DomainBus    *domainevents.Bus
	EventCounter *observ.EventCounter
	Logger       *slog.Logger
}

// Options bundles the inputs the bootstrap needs. Empty fields receive
// sensible defaults (sqlite path under $HOME/.tokenops, DefaultTable
// pricing, OpenAI/Anthropic/Gemini heuristic tokenizers).
type Options struct {
	// DBPath overrides the events.db location. Empty defaults to
	// $HOME/.tokenops/events.db.
	DBPath string
	// PricingPath points at a YAML rate-override file layered on top of
	// the built-in pricing catalog (config: pricing.path). Empty uses
	// the embedded list prices only.
	PricingPath string
	// Logger is the structured logger threaded through every component.
	// nil falls back to slog.Default().
	Logger *slog.Logger
	// OpenStore controls whether the sqlite store is opened. Set to
	// false for ephemeral / read-only adapters (e.g. unit tests).
	OpenStore bool
}

// Close releases every collaborator held by c. Idempotent.
func (c *Components) Close() error { return c.Shutdown() }

// OpenStoreAt opens (or replaces) the sqlite store at path and wires
// the dependent Aggregator without re-allocating bus/counter/redactor.
// Daemon flow: New(OpenStore:false) → optionally OpenStoreAt(path).
// Caller adapters that don't need persistence skip the second call.
func (c *Components) OpenStoreAt(ctx context.Context, path string) error {
	if c == nil {
		return errors.New("bootstrap: nil Components")
	}
	if c.Store != nil {
		return errors.New("bootstrap: store already open; close first")
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("bootstrap: resolve home dir: %w", err)
		}
		path = filepath.Join(home, ".tokenops", "events.db")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("bootstrap: create db dir: %w", err)
	}
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		return fmt.Errorf("bootstrap: open events.db at %s: %w", path, err)
	}
	c.Store = store
	c.Aggregator = analytics.New(store, c.Spend)
	return nil
}

// Shutdown orchestrates ordered teardown. Callers should prefer this
// over Close so future collaborators (event bus drain, OTLP flush) plug
// in here without changing call sites.
func (c *Components) Shutdown() error {
	if c == nil {
		return nil
	}
	if c.EventCounter != nil {
		c.EventCounter.Reset()
	}
	if c.Store != nil {
		if err := c.Store.Close(); err != nil {
			return err
		}
		c.Store = nil
	}
	return nil
}

// New constructs the long-lived collaborators in a fixed order so caller
// adapters never need to know the dependency graph.
func New(ctx context.Context, opts Options) (*Components, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	spendEng, err := buildSpendEngine(opts.PricingPath, opts.Logger)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	dbus := &domainevents.Bus{}
	counter := observ.NewEventCounter()
	counter.Subscribe(dbus)
	c := &Components{
		Logger:       opts.Logger,
		Spend:        spendEng,
		Tokenizers:   tokenizer.NewRegistry(),
		Redactor:     redaction.New(redaction.Config{}),
		DomainBus:    dbus,
		EventCounter: counter,
	}
	if !opts.OpenStore {
		return c, nil
	}
	path := opts.DBPath
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("bootstrap: resolve home dir: %w", err)
		}
		path = filepath.Join(home, ".tokenops", "events.db")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("bootstrap: create db dir: %w", err)
	}
	store, err := sqlite.Open(ctx, path, sqlite.Options{})
	if err != nil {
		return nil, fmt.Errorf("bootstrap: open events.db at %s: %w", path, err)
	}
	c.Store = store
	c.Aggregator = analytics.New(store, c.Spend)
	return c, nil
}

// buildSpendEngine constructs the effective-dated cost engine (ADR 0002
// Phase 2): events are priced at the rate card that was in effect at their
// timestamp, using the embedded baseline plus any persisted pricing
// snapshots under ~/.tokenops/pricing, with the negotiated-rate override
// file (pricingPath) layered across every period.
//
// It fails soft: any error building the effective-dated engine (or parsing
// the override file for it) falls back to the flat baseline+override engine
// so costing never breaks. A malformed override file is still a hard error —
// that is operator misconfiguration, surfaced exactly as before.
func buildSpendEngine(pricingPath string, logger *slog.Logger) (*spend.Engine, error) {
	// Flat engine over baseline + overrides — the guaranteed fallback and
	// the source of truth for override-file validity.
	flatTable, err := spend.TableWithOverrides(pricingPath)
	if err != nil {
		return nil, err
	}
	fallback := spend.NewEngine(flatTable)

	// Isolate just the override rows so they can be layered onto every dated
	// table. TableWithOverrides already validated the file above, so this
	// load cannot fail; guard anyway and degrade to the fallback.
	overrides := spend.Table{}
	if pricingPath != "" {
		ov, oerr := spend.LoadTableFile(pricingPath)
		if oerr != nil {
			return fallback, nil
		}
		overrides = ov
	}

	// Default pricing snapshot dir (~/.tokenops/pricing) via ResolveDir("").
	eng, eerr := pricing.EffectiveEngineWithOverrides("", overrides)
	if eerr != nil || eng == nil {
		if logger != nil {
			logger.Warn("effective-dated pricing unavailable; using flat baseline", "err", eerr)
		}
		return fallback, nil
	}
	return eng, nil
}

// ErrStoreUnavailable is returned by adapters that require an open store
// but received a Components with Store == nil.
var ErrStoreUnavailable = errors.New("bootstrap: events store not available")
