package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerWarp registers the reader (readers_gen.go).
func readerWarp() usage.Reader { return Warp{} }

// Warp reads the credits used since the last refresh from Warp's GraphQL
// API (GetRequestLimitInfo on app.warp.dev/graphql/v2), with an API key
// from Warp's settings (https://docs.warp.dev/reference/cli/api-keys), as
// CodexBar's Warp provider does. The fields are request-named, the figures
// are credits. Warp's edge refuses requests that do not name its client,
// so the request carries the client headers Warp's app sends. An
// unlimited plan has no share to report. Add-on (bonus) credits, the
// user's and each workspace's grants together, are a pool with no window:
// they are the credits balance left, in credits, never dollars.
type Warp struct {
	BaseURL string
	HTTP    *http.Client
}

func (Warp) Endpoint() string               { return "warp" }
func (Warp) Provider() eventschema.Provider { return "warp" }
func (Warp) Source() string                 { return "warp-account" }

const warpQuery = `query GetRequestLimitInfo($requestContext: RequestContext!) {
  user(requestContext: $requestContext) {
    __typename
    ... on UserOutput {
      user {
        requestLimitInfo {
          isUnlimited
          nextRefreshTime
          requestLimit
          requestsUsedSinceLastRefresh
        }
        bonusGrants {
          requestCreditsGranted
          requestCreditsRemaining
          expiration
        }
        workspaces {
          bonusGrantsInfo {
            grants {
              requestCreditsGranted
              requestCreditsRemaining
              expiration
            }
          }
        }
      }
    }
  }
}`

func (w Warp) Read(ctx context.Context, key string) (usage.Reading, error) {
	osName := map[string]string{"darwin": "macOS", "linux": "Linux", "windows": "Windows"}[runtime.GOOS]
	header := http.Header{
		"Authorization":      {"Bearer " + key},
		"User-Agent":         {"Warp/1.0"},
		"X-Warp-Client-Id":   {"warp-app"},
		"X-Warp-Os-Category": {osName},
		"X-Warp-Os-Name":     {osName},
	}
	payload := map[string]any{
		"query":         warpQuery,
		"operationName": "GetRequestLimitInfo",
		"variables": map[string]any{"requestContext": map[string]any{
			"clientContext": map[string]any{},
			"osContext":     map[string]any{"category": osName, "name": osName},
		}},
	}
	var resp struct {
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
		Data *struct {
			User *struct {
				User *struct {
					RequestLimitInfo *struct {
						IsUnlimited     bool   `json:"isUnlimited"`
						NextRefreshTime string `json:"nextRefreshTime"`
						RequestLimit    number `json:"requestLimit"`
						RequestsUsed    number `json:"requestsUsedSinceLastRefresh"`
					} `json:"requestLimitInfo"`
					BonusGrants []warpGrant `json:"bonusGrants"`
					Workspaces  []struct {
						BonusGrantsInfo *struct {
							Grants []warpGrant `json:"grants"`
						} `json:"bonusGrantsInfo"`
					} `json:"workspaces"`
				} `json:"user"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := postJSON(ctx, w.HTTP, base(w.BaseURL, "https://app.warp.dev")+"/graphql/v2?op=GetRequestLimitInfo", header, payload, &resp); err != nil {
		return usage.Reading{}, err
	}
	if len(resp.Errors) > 0 {
		e := strings.ToLower(resp.Errors[0].Message + " " + resp.Errors[0].Extensions.Code)
		if strings.Contains(e, "unauthenticated") || strings.Contains(e, "unauthorized") || strings.Contains(e, "forbidden") {
			return usage.Reading{}, fmt.Errorf("%w (GraphQL refusal on app.warp.dev/graphql/v2)", usage.ErrAuth)
		}
		return usage.Reading{}, errors.New("accounts: POST app.warp.dev/graphql/v2: GraphQL error")
	}
	if resp.Data == nil || resp.Data.User == nil || resp.Data.User.User == nil || resp.Data.User.User.RequestLimitInfo == nil {
		return usage.Reading{}, errors.New("accounts: POST app.warp.dev/graphql/v2: no requestLimitInfo in the answer")
	}
	user := resp.Data.User.User
	info := user.RequestLimitInfo
	r := usage.Reading{Scope: "account"}
	grants := user.BonusGrants
	for _, ws := range user.Workspaces {
		if ws.BonusGrantsInfo != nil {
			grants = append(grants, ws.BonusGrantsInfo.Grants...)
		}
	}
	if left, ok := warpAddOnCredits(grants); ok {
		r.Credits, r.CreditsUnit, r.HasCredits = left, "credits", true
	}
	if info.IsUnlimited || !info.RequestLimit.ok {
		return r, nil
	}
	used := 100.0
	if info.RequestLimit.v > 0 {
		used = clampPct(pct(info.RequestsUsed.v, info.RequestLimit.v))
	}
	r.Subscription = true
	r.Windows = []usage.Window{{Name: "credits", UsedPct: used, ResetsAt: parseTime(info.NextRefreshTime)}}
	return r, nil
}

// warpGrant is one add-on (bonus) credit grant.
type warpGrant struct {
	Granted   number `json:"requestCreditsGranted"`
	Remaining number `json:"requestCreditsRemaining"`
}

// warpAddOnCredits is the add-on credits left across every grant, summed
// as CodexBar sums them; false when the account has no grant at all.
func warpAddOnCredits(grants []warpGrant) (float64, bool) {
	left, seen := 0.0, false
	for _, g := range grants {
		if !g.Remaining.ok && !g.Granted.ok {
			continue
		}
		seen = true
		left += max(0, g.Remaining.v)
	}
	return left, seen
}
