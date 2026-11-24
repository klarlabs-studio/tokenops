package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerGeminiWeb registers the reader (readers_gen.go).
func readerGeminiWeb() usage.Reader { return GeminiWeb{} }

// GeminiWeb reads the Gemini app's 5-hour and weekly limits that Google
// AI Pro and Ultra have, ported from CodexBar (Sources/CodexBarCore/
// Resources/Plugins/gemini-web.ts, 0ff1cf55c0): the UsagePageQuery the
// gemini.google.com/usage page runs, with the browser's Google session.
// Google publishes no API for it, and it is not Gemini CLI's quota.
//
// Its endpoint is its own, "gemini-web", so a Gemini API key is never sent
// to it. percentageUsed is a fraction; a pool without one is absent, never
// zero.
type GeminiWeb struct {
	BaseURL string
	HTTP    *http.Client
}

func (GeminiWeb) Endpoint() string               { return "gemini-web" }
func (GeminiWeb) Provider() eventschema.Provider { return eventschema.ProviderGemini }
func (GeminiWeb) Source() string                 { return "gemini-web" }

// geminiUsageQuery is UsagePageQuery's signature and variables, from the
// public UsagePageSurface bundle.
const (
	geminiUsageSignature = "2/dWbCvkVB0X16r9NPnxifeOAAqdCqMf0xeWvau9yPy7g="
	geminiUsageVariables = `{"g1Request":{"entrypoints":["GEMINI_USAGE_SETTINGS_UPGRADE_BANNER"]},"workspaceRequest":{"recommendationsForSurfaceRequests":[]}}`
)

var errGeminiWebShape = errors.New("accounts: gemini app usage: unrecognised answer")

func (g GeminiWeb) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (the Gemini app credential is not a Cookie header)", usage.ErrAuth)
	}
	origin := base(g.BaseURL, "https://gemini.google.com")
	u := origin + "/_/BardChatUi/graphql/schemas/GEMINI_WEB_GRAPHQL/executeQuery?query_signature=" +
		url.QueryEscape(geminiUsageSignature) + "&variables=" + url.QueryEscape(geminiUsageVariables)
	body, err := doWeb(ctx, g.HTTP, http.MethodGet, u, http.Header{
		"Cookie": {cookie}, "Accept": {"application/json"}, "Referer": {origin + "/usage"},
	}, nil)
	if err != nil {
		return usage.Reading{}, err
	}
	body = bytes.TrimSpace(body)
	if bytes.HasPrefix(body, []byte("<")) {
		return usage.Reading{}, fmt.Errorf("%w (Gemini answered a sign-in page)", usage.ErrAuth)
	}
	body = bytes.TrimSpace(bytes.TrimPrefix(body, []byte(")]}'")))
	var results []struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &results) != nil {
		return usage.Reading{}, errGeminiWebShape
	}
	var viewer json.RawMessage
	for _, r := range results {
		if v, ok := r.Data["viewer"]; ok {
			viewer = v
			break
		}
	}
	switch {
	case viewer == nil:
		return usage.Reading{}, errGeminiWebShape
	case string(viewer) == "null":
		return usage.Reading{}, fmt.Errorf("%w (Gemini: signed out)", usage.ErrAuth)
	}
	var v struct {
		RPCs *struct {
			Usage *struct {
				Pools *[]struct {
					Type    string   `json:"poolType"`
					Percent *float64 `json:"percentageUsed"`
					GXU     *struct {
						Refresh string `json:"windowRefreshesAt"`
					} `json:"gxu"`
				} `json:"pools"`
			} `json:"getUsageInfo"`
		} `json:"rpcs"`
	}
	if json.Unmarshal(viewer, &v) != nil || v.RPCs == nil || v.RPCs.Usage == nil || v.RPCs.Usage.Pools == nil {
		return usage.Reading{}, errGeminiWebShape
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	for _, want := range []struct {
		pool string
		d    time.Duration
	}{{"GXU_FIVE_HOURLY", 5 * time.Hour}, {"GXU_WEEKLY", 7 * 24 * time.Hour}} {
		for _, p := range *v.RPCs.Usage.Pools {
			if p.Type != want.pool || p.Percent == nil || *p.Percent < 0 {
				continue
			}
			w := usage.Window{Name: windowName(want.d), UsedPct: min(100, *p.Percent*100), Duration: want.d}
			if p.GXU != nil {
				w.ResetsAt = parseTime(p.GXU.Refresh)
			}
			r.Windows = append(r.Windows, w)
			break
		}
	}
	if len(r.Windows) == 0 {
		return usage.Reading{}, errors.New("accounts: gemini app: this Google account reports no usage windows")
	}
	return r, nil
}
