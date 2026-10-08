package accounts

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerHuggingFace registers the reader (readers_gen.go).
func readerHuggingFace() usage.Reader { return HuggingFace{} }

// HuggingFace reads the month's Inference Providers charges from GET
// /api/settings/billing/usage-v2, as CodexBar does: the endpoint is in
// Hugging Face's OpenAPI spec but its answer is not documented. The
// charge is what the billing page shows, usedNanoUsd less includedNanoUsd
// (never below zero), against limitNanoUsd when one is set. periodEnd is
// the query's end, not a reset. The token needs billing read (a classic
// read token, or Billing read on a fine-grained one).
type HuggingFace struct {
	BaseURL string
	HTTP    *http.Client
	// Now ends the month-to-date query; tests fix it.
	Now func() time.Time
}

func (HuggingFace) Endpoint() string               { return "huggingface" }
func (HuggingFace) Provider() eventschema.Provider { return "huggingface" }
func (HuggingFace) Source() string                 { return "huggingface-account" }

func (h HuggingFace) Read(ctx context.Context, key string) (usage.Reading, error) {
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	end := now().UTC()
	start := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
	u := base(h.BaseURL, "https://huggingface.co") + "/api/settings/billing/usage-v2?startDate=" +
		strconv.FormatInt(start.Unix(), 10) + "&endDate=" + strconv.FormatInt(end.Unix(), 10)
	var resp struct {
		Usage *struct {
			Inference *struct {
				Used     number `json:"usedNanoUsd"`
				Included number `json:"includedNanoUsd"`
				Limit    number `json:"limitNanoUsd"`
			} `json:"inferenceProviders"`
		} `json:"usage"`
	}
	if err := getJSON(ctx, h.HTTP, u, key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Usage == nil || resp.Usage.Inference == nil || !resp.Usage.Inference.Used.ok {
		return usage.Reading{}, errors.New("accounts: huggingface billing: unrecognised answer")
	}
	inf := resp.Usage.Inference
	r := usage.Reading{Scope: "account", HasUsed: true, UsedUSD: math.Max(0, inf.Used.v-inf.Included.v) / 1e9}
	if inf.Limit.ok && inf.Limit.v > 0 {
		r.LimitUSD = inf.Limit.v / 1e9
	}
	return r, nil
}
