package providersetup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type fakeReader struct {
	id, good string
	reading  usage.Reading
}

func (f fakeReader) Endpoint() string               { return f.id }
func (f fakeReader) Provider() eventschema.Provider { return eventschema.Provider(f.id) }
func (f fakeReader) Source() string                 { return f.id + "-account" }
func (f fakeReader) Read(_ context.Context, key string) (usage.Reading, error) {
	if key != f.good {
		return usage.Reading{}, usage.ErrAuth
	}
	return f.reading, nil
}

func TestLookupCoversKeyProvidersOnly(t *testing.T) {
	p, ok := Lookup("OpenRouter")
	if !ok || p.ID != "openrouter" || p.Browser || len(p.EnvVars) == 0 {
		t.Errorf("openrouter = %+v %v", p, ok)
	}
	for _, id := range []string{"anthropic", "cerebras", "nope"} {
		if _, ok := Lookup(id); ok {
			t.Errorf("%s: setup does not connect it generically", id)
		}
	}
	// A gateway is connected by its address and a key.
	if p, ok := Lookup("sub2api"); !ok || !p.Gateway || p.BaseURLEnv != "SUB2API_BASE_URL" || len(p.EnvVars) != 1 {
		t.Errorf("sub2api = %+v %v", p, ok)
	}
	if p, ok := Lookup("openai"); !ok || !p.Admin || len(p.EnvVars) != 1 || p.EnvVars[0] != "OPENAI_ADMIN_KEY" {
		t.Errorf("openai = %+v %v", p, ok)
	}
	if ids := IDs(); len(ids) < 10 {
		t.Errorf("IDs = %v", ids)
	}
}

func TestVerifyReadsOnceAndSummarises(t *testing.T) {
	readers := []usage.Reader{fakeReader{id: "acme", good: "k", reading: usage.Reading{HasBalance: true, BalanceUSD: 7.5}}}
	lines, err := VerifyWith(context.Background(), readers, "acme", " k\n")
	if err != nil || len(lines) != 1 || lines[0] != "balance: $7.50" {
		t.Fatalf("%v %v", lines, err)
	}
	if _, err := VerifyWith(context.Background(), readers, "acme", "bad"); !errors.Is(err, ErrRefused) {
		t.Errorf("refused key = %v", err)
	}
	if _, err := VerifyWith(context.Background(), readers, "other", "k"); err == nil {
		t.Error("a provider with no reader verified")
	}
}

type fakeGateway struct{ name string }

func (f fakeGateway) Name() string                           { return f.name }
func (f fakeGateway) Source() string                         { return f.name + "-account" }
func (f fakeGateway) Recognise(context.Context, string) bool { return false }
func (f fakeGateway) Read(_ context.Context, root, key string) (usage.Reading, error) {
	if root != "https://gw.example/base" || key != "k" {
		return usage.Reading{}, usage.ErrAuth
	}
	return usage.Reading{HasUsed: true, UsedUSD: 2, LimitUSD: 10}, nil
}

func TestVerifyGatewayReadsAtTheAddressGiven(t *testing.T) {
	gws := []usage.Gateway{fakeGateway{name: "acme"}}
	lines, err := VerifyGatewayWith(context.Background(), gws, "acme", "https://gw.example/base/v1/", "k")
	if err != nil || len(lines) != 1 || lines[0] != "spend: $2.00 of $10.00" {
		t.Fatalf("%v %v", lines, err)
	}
	// The key is never sent in clear to a public host.
	if _, err := VerifyGatewayWith(context.Background(), gws, "acme", "http://gw.example", "k"); err == nil {
		t.Error("plain HTTP to a public host was accepted")
	}
	if _, err := VerifyGatewayWith(context.Background(), gws, "acme", "https://gw.example/base", "bad"); !errors.Is(err, ErrRefused) {
		t.Errorf("refused key = %v", err)
	}
	if _, err := VerifyGatewayWith(context.Background(), gws, "other", "https://gw.example/base", "k"); err == nil {
		t.Error("a gateway with no reader verified")
	}
	cfg := config.Default()
	ApplyGateway(&cfg, "acme", " https://gw.example/base ", " k ")
	if c := cfg.VendorUsage.Accounts.Credentials["acme"]; c.Key != "k" || c.BaseURL != "https://gw.example/base" {
		t.Errorf("stored %+v", c)
	}
}

func TestApplyStoresTheCredential(t *testing.T) {
	cfg := config.Default()
	Apply(&cfg, "acme", " sk \n", true, "Firefox")
	c := cfg.VendorUsage.Accounts.Credentials["acme"]
	if c.Key != "sk" || !c.FromBrowser || c.Browser != "Firefox" {
		t.Errorf("stored %+v", c)
	}
	if strings.Contains(string(mustSnapshot(t, cfg)), "\"sk\"") {
		t.Error("the stored key shows in the config snapshot")
	}
}

func mustSnapshot(t *testing.T, cfg config.Config) []byte {
	t.Helper()
	b, err := cfg.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
