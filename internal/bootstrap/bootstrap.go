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

	"github.com/felixgeelhaar/tokenops/internal/contexts/observability/analytics"
	"github.com/felixgeelhaar/tokenops/internal/contexts/observability/observ"
	"github.com/felixgeelhaar/tokenops/internal/contexts/prompts/tokenizer"
	"github.com/felixgeelhaar/tokenops/internal/contexts/security/redaction"
	"github.com/felixgeelhaar/tokenops/internal/contexts/spend/spend"
	"github.com/felixgeelhaar/tokenops/internal/domainevents"
	"github.com/felixgeelhaar/tokenops/internal/storage/sqlite"
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
	dbus := &domainevents.Bus{}
	counter := observ.NewEventCounter()
	counter.Subscribe(dbus)
	c := &Components{
		Logger:       opts.Logger,
		Spend:        spend.NewEngine(spend.DefaultTable()),
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

// ErrStoreUnavailable is returned by adapters that require an open store
// but received a Components with Store == nil.
var ErrStoreUnavailable = errors.New("bootstrap: events store not available")
