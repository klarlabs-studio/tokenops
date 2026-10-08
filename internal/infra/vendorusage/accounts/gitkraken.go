package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerGitKraken registers the reader (readers_gen.go).
func readerGitKraken() usage.Reader { return GitKraken{} }

// GitKraken reads GitKraken AI's weekly credits from GET
// /v1/ai-tasks/usage on api.gitkraken.dev, with the account session's
// bearer token (gitkraken.dev/account#ai-usage). The endpoint is not
// published; this follows CodexBar's GitKraken plugin, which follows
// GitLens's usage parser. The organization pool is read only when
// GITKRAKEN_ORG_ID names one (sent as gk-org-id).
type GitKraken struct {
	BaseURL string
	HTTP    *http.Client
	// OrgID overrides GITKRAKEN_ORG_ID, for tests.
	OrgID string
}

func (GitKraken) Endpoint() string               { return "gitkraken" }
func (GitKraken) Provider() eventschema.Provider { return "gitkraken" }
func (GitKraken) Source() string                 { return "gitkraken-account" }

// gitkrakenReset is the reset time the API reports: always with a zone.
var gitkrakenReset = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$`)

type gitkrakenQuota struct {
	Used  *float64 `json:"used"`
	Limit *float64 `json:"limit"`
}

// window is the quota as a weekly window; a limit of 0 (no allowance) or
// -1 (unlimited) has no percentage, so no window.
func (q gitkrakenQuota) window(name string, resets time.Time) (usage.Window, bool) {
	if q.Used == nil || q.Limit == nil || *q.Limit <= 0 {
		return usage.Window{}, false
	}
	return usage.Window{Name: name, UsedPct: math.Min(pct(*q.Used, *q.Limit), 100), Duration: 7 * 24 * time.Hour, ResetsAt: resets}, true
}

func (q gitkrakenQuota) valid() bool {
	return q.Used != nil && q.Limit != nil && *q.Used >= 0 && !math.IsInf(*q.Used, 0) &&
		(*q.Limit >= 0 || *q.Limit == -1) && !math.IsInf(*q.Limit, 0)
}

func (g GitKraken) Read(ctx context.Context, key string) (usage.Reading, error) {
	headers := map[string]string{"Authorization": "Bearer " + key, "Accept": "application/json"}
	org := g.OrgID
	if org == "" {
		org = strings.TrimSpace(os.Getenv("GITKRAKEN_ORG_ID"))
	}
	if org != "" {
		headers["gk-org-id"] = org
	}
	var resp struct {
		Data *struct {
			gitkrakenQuota
			ResetsOn     string          `json:"resetsOn"`
			Organization *gitkrakenQuota `json:"organization"`
		} `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	url := base(g.BaseURL, "https://api.gitkraken.dev") + "/v1/ai-tasks/usage"
	if err := sendJSON(ctx, g.HTTP, http.MethodGet, url, headers, nil, &resp); err != nil {
		return usage.Reading{}, err
	}
	if len(resp.Error) > 0 && string(resp.Error) != "null" {
		return usage.Reading{}, fmt.Errorf("accounts: GitKraken reported an error")
	}
	if resp.Data == nil || !resp.Data.valid() {
		return usage.Reading{}, fmt.Errorf("accounts: GitKraken usage in a shape this version cannot read")
	}
	var resets time.Time
	if gitkrakenReset.MatchString(resp.Data.ResetsOn) {
		resets = parseTime(resp.Data.ResetsOn)
	}
	r := usage.Reading{Scope: "account", Subscription: true}
	if w, ok := resp.Data.window("week", resets); ok {
		r.Windows = append(r.Windows, w)
	}
	// The organization's pool is shared; its usage is the whole team's,
	// never added to the personal figure.
	if o := resp.Data.Organization; o != nil && o.valid() {
		if w, ok := o.window("week (organization)", resets); ok {
			r.Windows = append(r.Windows, w)
		}
	}
	return r, nil
}
