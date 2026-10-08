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

// A balance in the vendor's own unit is stored as reported, never as
// dollars, and makes the reading worth storing.
func TestCreditsAreStoredInTheirOwnUnit(t *testing.T) {
	x := Reading{Scope: "account", Credits: 1250000, CreditsUnit: "points", HasCredits: true}
	if x.Empty() {
		t.Fatal("a points balance is not empty")
	}
	a := NewEnvelope(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), &fakeReader{endpoint: "poe"}, x).Attributes
	if a["balance_credits"] != "1250000" || a["balance_credits_unit"] != "points" {
		t.Errorf("attributes %v", a)
	}
	if _, ok := a["balance_usd"]; ok {
		t.Error("points were stored as dollars")
	}
}

// A gateway the operator named is read at the address given, by that
// gateway only, without a recognition probe; a public plain-HTTP address
// is never sent the key.
func TestPollerReadsANamedGatewayWhereItWasNamed(t *testing.T) {
	g := &fakeGateway{root: "never recognised"}
	p := NewPoller(nil, PollerOptions{Gateways: []Gateway{g}, Credentials: func() []Credential {
		return []Credential{
			{Endpoint: GatewayEndpoint, BaseURL: "https://gw.example/team/v1/", Key: "vk", Gateway: "litellm"},
			{Endpoint: GatewayEndpoint, BaseURL: "http://gw.example", Key: "secret", Gateway: "litellm"},
			{Endpoint: GatewayEndpoint, BaseURL: "https://other.example", Key: "secret", Gateway: "unknown"},
		}
	}})
	p.Scan(context.Background())
	if g.recognised != 0 {
		t.Errorf("a named gateway was probed %d times", g.recognised)
	}
	if len(g.reads) != 1 || g.reads[0] != "https://gw.example/team|vk" {
		t.Fatalf("reads %v", g.reads)
	}
}

func TestNamedGatewayBase(t *testing.T) {
	for raw, want := range map[string]string{
		"https://gw.example/v1":         "https://gw.example",
		"https://gw.example/prefix/v1/": "https://gw.example/prefix",
		"http://127.0.0.1:8080":         "http://127.0.0.1:8080",
		"http://192.168.1.4/v1":         "http://192.168.1.4",
		"http://sub2api.local:3000":     "http://sub2api.local:3000",
		"http://[::1]:4000":             "http://[::1]:4000",
		"http://gw.example":             "",
		"https://user:pw@gw.example":    "",
		"https://gw.example/?debug=1":   "",
		"https://gw.example/#x":         "",
		"ftp://gw.example":              "",
		"gw.example":                    "",
	} {
		got, ok := NamedGatewayBase(raw)
		if got != want || ok != (want != "") {
			t.Errorf("%s: got %q %v, want %q", raw, got, ok, want)
		}
	}
}

type pacedReader struct {
	fakeReader
	every time.Duration
}

func (p *pacedReader) MinInterval() time.Duration { return p.every }

// A paced reader (a vendor that bills each request) is asked no more often
// than its interval, refused or not; with no credential it is not asked
// at all and its interval does not start.
func TestPollerPacesAReaderThatIsBilledPerRequest(t *testing.T) {
	r := &pacedReader{fakeReader: fakeReader{endpoint: "aws", good: "k", reading: Reading{HasUsed: true, UsedUSD: 1}}, every: 8 * time.Hour}
	at := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	creds := []Credential{}
	p := NewPoller(&captureBus{}, PollerOptions{Readers: []Reader{r}, Credentials: func() []Credential { return creds }, Now: func() time.Time { return at }})
	p.Scan(context.Background())
	creds = []Credential{{Endpoint: "aws", Key: "bad"}}
	for _, step := range []time.Duration{0, time.Hour, 6 * time.Hour} {
		at = at.Add(step)
		p.Scan(context.Background())
	}
	if len(r.keys) != 1 {
		t.Fatalf("asked %d times within 8 hours: %v", len(r.keys), r.keys)
	}
	at = at.Add(time.Hour)
	p.Scan(context.Background())
	if len(r.keys) != 2 {
		t.Errorf("not asked again after its interval: %v", r.keys)
	}
}
