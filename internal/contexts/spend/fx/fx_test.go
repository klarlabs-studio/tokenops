package fx

import (
	"math"
	"testing"
)

const ecbSample = `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
	<gesmes:subject>Reference rates</gesmes:subject>
	<Cube>
		<Cube time='2026-09-30'>
			<Cube currency='USD' rate='1.1355'/>
			<Cube currency='GBP' rate='0.8600'/>
			<Cube currency='CHF' rate='0.9400'/>
		</Cube>
	</Cube>
</gesmes:Envelope>`

func TestParseECBAndConvert(t *testing.T) {
	e, err := ParseECB([]byte(ecbSample))
	if err != nil || e.Date != "2026-09-30" {
		t.Fatalf("ParseECB = %+v, %v", e, err)
	}
	eur, ok := e.Rate("eur")
	if !ok || math.Abs(eur.PerUSD-1/1.1355) > 1e-9 || eur.Source != SourceECB {
		t.Fatalf("EUR rate = %+v", eur)
	}
	if got := eur.FromUSD(1135.5); math.Abs(got-1000) > 1e-6 {
		t.Errorf("1135.50 USD = %.4f EUR, want 1000", got)
	}
	gbp, ok := e.Rate("GBP")
	if !ok || math.Abs(gbp.PerUSD-0.86/1.1355) > 1e-9 {
		t.Errorf("GBP rate = %+v", gbp)
	}
	if _, ok := e.Rate("XYZ"); ok {
		t.Error("rate for a currency the ECB does not publish")
	}
	if note := eur.Note(); note != "1 EUR = 1.1355 USD (ECB reference rate, 2026-09-30)" {
		t.Errorf("Note = %q", note)
	}
}

func TestParseECBRejectsGarbage(t *testing.T) {
	if _, err := ParseECB([]byte("<html>maintenance</html>")); err == nil {
		t.Fatal("parsed a page with no rates")
	}
}

func TestRegionFromLocale(t *testing.T) {
	cases := map[string]string{
		"en_US@rg=dezzzz": "DE", // macOS: English system, German region
		"de_DE.UTF-8":     "DE",
		"en_GB":           "GB",
		"en-US":           "US",
		"C":               "",
	}
	for in, want := range cases {
		if got := RegionFromLocale(in); got != want {
			t.Errorf("RegionFromLocale(%q) = %q, want %q", in, got, want)
		}
	}
	if c, _ := CurrencyForRegion("de"); c != "EUR" {
		t.Errorf("DE = %q", c)
	}
}

func TestConfigRateNote(t *testing.T) {
	r := Rate{Currency: "EUR", PerUSD: 0.88, Source: SourceConfig}
	if r.Note() != "1 USD = 0.8800 EUR (your rate)" || USD.Note() != "" {
		t.Errorf("notes: %q / %q", r.Note(), USD.Note())
	}
}
