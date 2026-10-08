package accounts

import (
	"context"
	"reflect"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestOpenRouter(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		body string
		want usage.Reading
	}{
		"no cap": {`{"data":{"limit":null,"limit_reset":null,"limit_remaining":null,"usage":90,"usage_monthly":12.5}}`,
			usage.Reading{Scope: "key", UsedUSD: 12.5, HasUsed: true}},
		"monthly cap": {fixture(t, "openrouter"),
			usage.Reading{Scope: "key", UsedUSD: 12.5, HasUsed: true, LimitUSD: 50}},
		"lifetime cap spent": {`{"data":{"limit":100,"limit_reset":null,"limit_remaining":0,"usage":100,"usage_monthly":3}}`,
			usage.Reading{Scope: "key", UsedUSD: 100, HasUsed: true, LimitUSD: 100, LimitReached: true}},
	} {
		srv := serve(t, "/api/v1/key", "sk-or", tc.body)
		got, err := OpenRouter{BaseURL: srv.URL}.Read(ctx, "sk-or")
		srv.Close()
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v %v, want %+v", name, got, err, tc.want)
		}
	}
}
