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
	for _, id := range []string{"anthropic", "litellm", "cerebras", "nope"} {
		if _, ok := Lookup(id); ok {
			t.Errorf("%s: setup does not connect it generically", id)
		}
	}
	if ids := IDs(); len(ids) < 10 {
		t.Errorf("IDs = %v", ids)
	}
}

// A credential that is more than one key says what to paste.
func TestLookupSaysTheKeyFormat(t *testing.T) {
	if p, ok := Lookup("xai"); !ok || p.KeyFormat != "TEAM_ID:MANAGEMENT_KEY" {
		t.Errorf("xai = %+v %v", p, ok)
	}
	if p, _ := Lookup("openrouter"); p.KeyFormat != "" {
		t.Errorf("openrouter asks for %q", p.KeyFormat)
	}
}

// A browser session is read from the browser when its cookies are known
// by name, and pasted when they are not.
func TestLookupBrowserSessions(t *testing.T) {
	if p, ok := Lookup("manus"); !ok || !p.Browser || p.PasteCookie || p.CookieHost != "manus.im" || len(p.CookieNames) != 1 {
		t.Errorf("manus = %+v %v", p, ok)
	}
	p, ok := Lookup("t3chat")
	if !ok || p.Browser || !p.PasteCookie || p.CookieHost != "t3.chat" {
		t.Errorf("t3chat = %+v %v", p, ok)
	}
	if _, _, err := FromBrowser(context.Background(), p, "", 0, true); err == nil {
		t.Error("a pasted session was read from a browser")
	}
}

// A balance in the vendor's own unit is worded in that unit.
func TestSummaryWordsCreditsInTheirUnit(t *testing.T) {
	got := Summary(usage.Reading{Credits: 1500, CreditsUnit: "points", HasCredits: true})
	if len(got) != 1 || got[0] != "balance: 1500 points" {
		t.Errorf("summary %v", got)
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
