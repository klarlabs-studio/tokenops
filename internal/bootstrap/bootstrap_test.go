package bootstrap

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNewWithoutStore(t *testing.T) {
	c, err := New(context.Background(), Options{OpenStore: false})
	if err != nil {
		t.Fatal(err)
	}
	if c.Store != nil {
		t.Error("store should be nil without OpenStore")
	}
	if c.Spend == nil {
		t.Error("spend missing")
	}
	if c.Tokenizers == nil {
		t.Error("tokenizers missing")
	}
	if c.Redactor == nil {
		t.Error("redactor missing")
	}
	if c.DomainBus == nil {
		t.Error("domain bus missing")
	}
	if c.EventCounter == nil {
		t.Error("event counter missing")
	}
}

func TestNewWithStoreThenShutdown(t *testing.T) {
	dir := t.TempDir()
	c, err := New(context.Background(), Options{
		DBPath:    filepath.Join(dir, "events.db"),
		OpenStore: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Store == nil {
		t.Fatal("store should be open")
	}
	if c.Aggregator == nil {
		t.Fatal("aggregator should be wired")
	}
	if err := c.Shutdown(); err != nil {
		t.Errorf("first shutdown: %v", err)
	}
	if c.Store != nil {
		t.Errorf("Store should be nil after Shutdown")
	}
	// Idempotent.
	if err := c.Shutdown(); err != nil {
		t.Errorf("second shutdown: %v", err)
	}
}

func TestOpenStoreAtUpgradesEarlyComponents(t *testing.T) {
	c, err := New(context.Background(), Options{OpenStore: false})
	if err != nil {
		t.Fatal(err)
	}
	if c.Store != nil {
		t.Fatal("expected store nil")
	}
	dir := t.TempDir()
	if err := c.OpenStoreAt(context.Background(), filepath.Join(dir, "events.db")); err != nil {
		t.Fatalf("OpenStoreAt: %v", err)
	}
	if c.Store == nil || c.Aggregator == nil {
		t.Error("expected store + aggregator after OpenStoreAt")
	}
	// Second call must error (store already open).
	if err := c.OpenStoreAt(context.Background(), filepath.Join(dir, "x.db")); err == nil {
		t.Error("expected error on double-open")
	}
	if err := c.Shutdown(); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestNilComponentsShutdownSafe(t *testing.T) {
	var c *Components
	if err := c.Shutdown(); err != nil {
		t.Errorf("nil shutdown: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("nil close: %v", err)
	}
}
