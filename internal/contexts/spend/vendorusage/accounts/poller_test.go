package accounts

import (
	"context"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type captureBus struct{ got []*eventschema.Envelope }

func (b *captureBus) Publish(env *eventschema.Envelope) { b.got = append(b.got, env) }
func (b *captureBus) PublishWait(_ context.Context, env *eventschema.Envelope) error {
	b.got = append(b.got, env)
	return nil
}
func (b *captureBus) DroppedCount() int64       { return 0 }
func (b *captureBus) PublishedCount() int64     { return int64(len(b.got)) }
func (b *captureBus) Close(time.Duration) error { return nil }

// fakeReader accepts one key and records every key it was sent.
type fakeReader struct {
	endpoint, good string
	reading        Reading
	keys           []string
}

func (f *fakeReader) Endpoint() string               { return f.endpoint }
func (f *fakeReader) Provider() eventschema.Provider { return eventschema.Provider(f.endpoint) }
func (f *fakeReader) Source() string                 { return f.endpoint + "-account" }
func (f *fakeReader) Read(_ context.Context, key string) (Reading, error) {
	f.keys = append(f.keys, key)
	if key != f.good {
		return Reading{}, ErrAuth
	}
	return f.reading, nil
}

// The poller hands each reader only its own vendor's keys, tries them in
// order, and stores an unchanged reading once.
func TestPollerTriesOnlyMatchingKeys(t *testing.T) {
	or := &fakeReader{endpoint: "openrouter", good: "sk-good", reading: Reading{Scope: "key", UsedUSD: 4, HasUsed: true}}
	ds := &fakeReader{endpoint: "deepseek"}
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{
		Readers: []Reader{or, ds},
		Credentials: func() []Credential {
			return []Credential{
				{Endpoint: "openrouter", Key: "sk-stale"},
				{Endpoint: "fireworks", Key: "sk-good"}, // another vendor's key is never sent
				{Endpoint: "openrouter", Key: "sk-good"},
			}
		},
		Now: func() time.Time { return time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC) },
	})
	p.Scan(context.Background())
	if len(ds.keys) != 0 {
		t.Errorf("DeepSeek was sent %v with no DeepSeek key", ds.keys)
	}
	if len(or.keys) != 2 || or.keys[0] != "sk-stale" || or.keys[1] != "sk-good" {
		t.Errorf("OpenRouter was sent %v, want its own keys in order", or.keys)
	}
	if len(bus.got) != 1 || bus.got[0].Source != "openrouter-account" || bus.got[0].Attributes["extra_usage_used"] != "4.00" {
		t.Fatalf("published %+v", bus.got)
	}
	p.Scan(context.Background())
	if len(bus.got) != 1 {
		t.Errorf("an unchanged reading was stored again: %d", len(bus.got))
	}
}

// fakeGateway is recognised at one root and records what it was asked.
type fakeGateway struct {
	root       string
	recognised int
	reads      []string
}

func (*fakeGateway) Name() string   { return "litellm" }
func (*fakeGateway) Source() string { return "litellm-account" }
func (g *fakeGateway) Recognise(_ context.Context, root string) bool {
	g.recognised++
	return root == g.root
}
func (g *fakeGateway) Read(_ context.Context, root, key string) (Reading, error) {
	g.reads = append(g.reads, root+"|"+key)
	return Reading{Scope: "key", UsedUSD: 3, HasUsed: true, LimitUSD: 10}, nil
}

// A gateway key is read at the root the harness sends it to, only once
// that root is recognised, and recognition is cached between scans.
func TestPollerSendsAGatewayKeyOnlyWhereItBelongs(t *testing.T) {
	g := &fakeGateway{root: "http://lite.local:4000"}
	bus := &captureBus{}
	p := NewPoller(bus, PollerOptions{Gateways: []Gateway{g}, Credentials: func() []Credential {
		return []Credential{
			{Endpoint: GatewayEndpoint, BaseURL: "http://lite.local:4000/anthropic", Key: "vk"},
			{Endpoint: GatewayEndpoint, BaseURL: "https://elsewhere.example/v1", Key: "secret"},
			{Endpoint: GatewayEndpoint, BaseURL: "not a url", Key: "secret"},
		}
	}})
	p.Scan(context.Background())
	if len(g.reads) != 1 || g.reads[0] != "http://lite.local:4000|vk" {
		t.Fatalf("gateway reads %v, want one at its root with its key", g.reads)
	}
	if len(bus.got) != 1 || bus.got[0].Source != "litellm-account" {
		t.Fatalf("published %+v", bus.got)
	}
	asked := g.recognised
	p.Scan(context.Background())
	if g.recognised != asked {
		t.Errorf("recognition re-probed: %d then %d", asked, g.recognised)
	}
}

// A count with no allowance is stored as a count, never as a percentage.
func TestEnvelopeCarriesCounts(t *testing.T) {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	r := Reading{Scope: "account", Subscription: true, Counts: []Count{{Name: "reviews", Used: 25, ResetsAt: at}}}
	if r.Empty() {
		t.Fatal("a reading with a count is empty")
	}
	a := NewEnvelope(at, &fakeLocal{}, r).Attributes
	if a["count_0_name"] != "reviews" || a["count_0_used"] != "25" || a["count_0_reset_at"] != "2026-09-01T00:00:00Z" {
		t.Errorf("attributes %v", a)
	}
	if _, ok := a["window_0_used_pct"]; ok {
		t.Error("a count became a window")
	}
}

// fakeLocal is a keyless reader: a vendor CLI, installed or not.
type fakeLocal struct {
	installed bool
	keys      []string
}

func (*fakeLocal) Endpoint() string               { return "acme" }
func (*fakeLocal) Provider() eventschema.Provider { return "acme" }
func (*fakeLocal) Source() string                 { return "acme-cli" }
func (*fakeLocal) Keyless()                       {}
func (f *fakeLocal) Read(_ context.Context, key string) (Reading, error) {
	f.keys = append(f.keys, key)
	if !f.installed {
		return Reading{}, ErrNotInstalled
	}
	return Reading{Scope: "account", Subscription: true, Windows: []Window{{Name: "month", UsedPct: 40}}}, nil
}

// A keyless reader is read with no credential at all, never handed one
// found for its vendor, and skipped silently when it is not installed.
func TestPollerReadsKeylessReadersWithoutAKey(t *testing.T) {
	absent, present := &fakeLocal{}, &fakeLocal{installed: true}
	bus := &captureBus{}
	creds := func() []Credential { return []Credential{{Endpoint: "acme", Key: "secret"}} }
	NewPoller(bus, PollerOptions{Readers: []Reader{absent}, Credentials: creds}).Scan(context.Background())
	if len(bus.got) != 0 {
		t.Fatalf("an uninstalled CLI published %+v", bus.got)
	}
	NewPoller(bus, PollerOptions{Readers: []Reader{present}, Credentials: creds}).Scan(context.Background())
	if len(present.keys) != 1 || present.keys[0] != "" {
		t.Errorf("keyless reader was sent %q", present.keys)
	}
	if len(bus.got) != 1 || bus.got[0].Source != "acme-cli" || bus.got[0].Attributes["window_0_used_pct"] != "40.00" {
		t.Fatalf("published %+v", bus.got)
	}
}
