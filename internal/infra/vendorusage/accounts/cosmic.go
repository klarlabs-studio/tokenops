package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerCosmic registers the reader (readers_gen.go).
func readerCosmic() usage.Reader { return Cosmic{} }

// Cosmic reads a Cosmic project's AI tokens against its plan's allowance,
// ported from CodexBar (Sources/CodexBarCore/Resources/Plugins/cosmic.ts,
// Providers/Cosmic, docs/cosmic.md): GET dapi.cosmicjs.com/v3/projects/
// usage, the read-only route under the official CLI's `cosmic billing
// usage`, with a Personal Access Token. The credential is
// PROJECT_ID:PERSONAL_ACCESS_TOKEN. Input and output tokens are separate
// allowances; the answer has no reset, so none is claimed, and one-time
// token packs are not added to them.
type Cosmic struct {
	BaseURL string
	HTTP    *http.Client
}

func (Cosmic) Endpoint() string               { return "cosmic" }
func (Cosmic) Provider() eventschema.Provider { return "cosmic" }
func (Cosmic) Source() string                 { return "cosmic-project" }

var (
	errCosmicShape    = errors.New("accounts: cosmic project usage: unrecognised answer")
	cosmicAllowanceRe = lazyRegexp(`(?i)^(\d+(?:\.\d+)?)\s*([km]?)$`)
)

func (c Cosmic) Read(ctx context.Context, credential string) (usage.Reading, error) {
	project, token, ok := strings.Cut(strings.TrimSpace(credential), ":")
	project, token = strings.TrimSpace(project), strings.TrimSpace(token)
	if !ok || project == "" || token == "" {
		return usage.Reading{}, fmt.Errorf("%w (Cosmic needs PROJECT_ID:PERSONAL_ACCESS_TOKEN)", usage.ErrAuth)
	}
	var resp struct {
		Usage *struct {
			AI *struct {
				Input  json.RawMessage `json:"input_tokens"`
				Output json.RawMessage `json:"output_tokens"`
			} `json:"ai"`
		} `json:"usage"`
		PlanInfo *struct {
			AITokens *struct {
				MaxInput  json.RawMessage `json:"max_input"`
				MaxOutput json.RawMessage `json:"max_output"`
			} `json:"ai_tokens"`
		} `json:"plan_info"`
	}
	u := base(c.BaseURL, "https://dapi.cosmicjs.com") + "/v3/projects/usage?project_id=" + url.QueryEscape(project)
	if err := doJSON(ctx, c.HTTP, http.MethodGet, u, http.Header{
		"Authorization": {"Bearer " + token}, "Origin": {"https://app.cosmicjs.com"},
	}, nil, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.Usage == nil || resp.Usage.AI == nil {
		return usage.Reading{}, errCosmicShape
	}
	var maxIn, maxOut json.RawMessage
	if resp.PlanInfo != nil && resp.PlanInfo.AITokens != nil {
		maxIn, maxOut = resp.PlanInfo.AITokens.MaxInput, resp.PlanInfo.AITokens.MaxOutput
	}
	r := usage.Reading{Scope: "project", Subscription: true}
	found := false
	for _, d := range []struct {
		name      string
		used, max json.RawMessage
	}{{"input", resp.Usage.AI.Input, maxIn}, {"output", resp.Usage.AI.Output, maxOut}} {
		used, hasUsed, ok := optionalCount(d.used)
		if !ok {
			return usage.Reading{}, errCosmicShape
		}
		limit, hasLimit, ok := cosmicAllowance(d.max)
		if !ok {
			return usage.Reading{}, errCosmicShape
		}
		found = found || hasUsed || hasLimit
		switch {
		case hasUsed && hasLimit && limit > 0:
			r.Windows = append(r.Windows, usage.Window{Name: "tokens (" + d.name + ")", UsedPct: clampPct(pct(used, limit))})
		case hasUsed:
			r.Counts = append(r.Counts, usage.Count{Name: d.name + " tokens", Used: used})
		}
	}
	if !found {
		return usage.Reading{}, errCosmicShape
	}
	return r, nil
}

// cosmicAllowance reads an allowance given as a whole number or as a
// string with a k or M suffix ("1.5M"); an unrecognised string ("unlimited")
// is no allowance, not an error.
func cosmicAllowance(raw json.RawMessage) (v float64, present, ok bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return optionalCount(raw)
	}
	m := cosmicAllowanceRe().FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false, true
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false, false
	}
	switch strings.ToLower(m[2]) {
	case "m":
		n *= 1e6
	case "k":
		n *= 1e3
	}
	return n, true, true
}
