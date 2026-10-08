package accounts

import (
	"context"
	"errors"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerElevenLabs registers the reader (readers_gen.go).
func readerElevenLabs() usage.Reader { return ElevenLabs{} }

// ElevenLabs reads GET /v1/user/subscription with the key in xi-api-key
// (https://elevenlabs.io/docs/api-reference/user/subscription/get): the
// credits used of the period's limit, as a window resetting at
// next_character_count_reset_unix, its length from
// character_refresh_period. The key needs the user_read permission.
type ElevenLabs struct {
	BaseURL string
	HTTP    *http.Client
}

func (ElevenLabs) Endpoint() string               { return "elevenlabs" }
func (ElevenLabs) Provider() eventschema.Provider { return "elevenlabs" }
func (ElevenLabs) Source() string                 { return "elevenlabs-account" }

// elevenLabsPeriods are the documented refresh periods.
var elevenLabsPeriods = map[string]struct {
	name string
	d    time.Duration
}{
	"monthly_period": {"month", 30 * 24 * time.Hour},
	"3_month_period": {"quarter", 91 * 24 * time.Hour},
	"6_month_period": {"half-year", 182 * 24 * time.Hour},
	"annual_period":  {"year", 365 * 24 * time.Hour},
}

func (e ElevenLabs) Read(ctx context.Context, key string) (usage.Reading, error) {
	var resp struct {
		Used    *float64 `json:"character_count"`
		Limit   *float64 `json:"character_limit"`
		ResetAt float64  `json:"next_character_count_reset_unix"`
		Refresh string   `json:"character_refresh_period"`
	}
	u := base(e.BaseURL, "https://api.elevenlabs.io") + "/v1/user/subscription"
	if err := getJSONHeader(ctx, e.HTTP, u, "xi-api-key", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Used == nil || resp.Limit == nil {
		return usage.Reading{}, errors.New("accounts: elevenlabs subscription: no character_count or character_limit")
	}
	if *resp.Limit <= 0 {
		return usage.Reading{Scope: "account"}, nil
	}
	p, ok := elevenLabsPeriods[resp.Refresh]
	if !ok {
		p.name = "period" // no refresh period reported: its length is unknown
	}
	return usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{{
		Name: p.name, UsedPct: clampPct(pct(*resp.Used, *resp.Limit)), Duration: p.d, ResetsAt: unixTime(resp.ResetAt),
	}}}, nil
}
