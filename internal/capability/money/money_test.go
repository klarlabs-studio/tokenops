package money

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/spend/fx"
)

func TestShowAndFormat(t *testing.T) {
	eur := fx.Rate{Currency: "EUR", PerUSD: 0.5, Source: fx.SourceConfig}
	if d := Show(eur, 10, 100); d == nil || d.Cost != 5 || d.APIEquivalent != 50 {
		t.Fatalf("Show = %+v", d)
	}
	if s, ok := Format(eur, 3); !ok || s != "1.50 EUR" {
		t.Errorf("Format = %q %v", s, ok)
	}
	if Show(fx.USD, 1, 1) != nil {
		t.Error("converted USD to USD")
	}
	if _, ok := Format(fx.Rate{}, 1); ok {
		t.Error("formatted with no rate")
	}
}
