package accounts

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerDeepgram registers the reader (readers_gen.go).
func readerDeepgram() usage.Reader { return Deepgram{} }

// Deepgram lists the projects the key sees (GET /v1/projects) and adds up
// their outstanding USD balances (GET /v1/projects/{id}/balances,
// https://developers.deepgram.com/reference/manage/billing/list). The
// key is sent as "Token <key>"; reading balances needs a key with access
// to each project's billing. A balance in another unit (hours) is not
// dollars and is left out.
type Deepgram struct {
	BaseURL string
	HTTP    *http.Client
}

func (Deepgram) Endpoint() string               { return "deepgram" }
func (Deepgram) Provider() eventschema.Provider { return "deepgram" }
func (Deepgram) Source() string                 { return "deepgram-account" }

func (d Deepgram) Read(ctx context.Context, key string) (usage.Reading, error) {
	root := base(d.BaseURL, "https://api.deepgram.com") + "/v1/projects"
	auth := "Token " + key
	var projects struct {
		Projects []struct {
			ProjectID string `json:"project_id"`
		} `json:"projects"`
	}
	if err := getJSONAuth(ctx, d.HTTP, root, auth, &projects); err != nil {
		return usage.Reading{}, err
	}
	r := usage.Reading{Scope: "account"}
	for _, p := range projects.Projects {
		if p.ProjectID == "" {
			continue
		}
		var resp struct {
			Balances []struct {
				Amount number `json:"amount"`
				Units  string `json:"units"`
			} `json:"balances"`
		}
		if err := getJSONAuth(ctx, d.HTTP, root+"/"+url.PathEscape(p.ProjectID)+"/balances", auth, &resp); err != nil {
			return usage.Reading{}, err
		}
		for _, b := range resp.Balances {
			if b.Amount.ok && strings.EqualFold(b.Units, "usd") {
				r.BalanceUSD += b.Amount.v
				r.HasBalance = true
			}
		}
	}
	return r, nil
}
