package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerNotion registers the reader (readers_gen.go).
func readerNotion() usage.Reader { return Notion{} }

// Notion reads the Notion AI usage allowance Notion shows in Settings →
// Notion AI → Usage, ported from CodexBar
// (Sources/CodexBarCore/Resources/Plugins/notion.ts, Providers/Notion,
// docs/notion.md): app.notion.com's internal /api/v3 calls getSpaces, to
// find the workspace, and getCreditRateLimitStatus, with the browser's
// token_v2 session cookie. The credential is the Cookie header holding
// token_v2, or its bare value.
//
// The rolling allowance (6 hours today) is a window named by its length,
// the billing-period allowance the "month" window. The allowance exists on
// Business and Enterprise workspaces only; a workspace with none is
// preferred last, and one Notion does not track is an error, not zero use.
type Notion struct {
	BaseURL string
	HTTP    *http.Client
	// Now turns the rolling window's seconds-to-reset into a time; nil is
	// time.Now.
	Now func() time.Time
}

func (Notion) Endpoint() string               { return "notion" }
func (Notion) Provider() eventschema.Provider { return "notion" }
func (Notion) Source() string                 { return "notion-account" }

type notionWindow struct {
	Used     *number `json:"used"`
	Limit    *number `json:"limit"`
	Window   string  `json:"window"`
	PeriodMs stamp   `json:"periodEndMs"`
}

func (n Notion) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if !strings.ContainsAny(cookie, "=;") && cookie != "" {
		cookie = "token_v2=" + cookie
	}
	if cookieValue(cookie, "token_v2") == "" {
		return usage.Reading{}, fmt.Errorf("%w (no token_v2 in the Notion session)", usage.ErrAuth)
	}
	header := http.Header{
		"Cookie":     {cookie},
		"Origin":     {"https://app.notion.com"},
		"Referer":    {"https://app.notion.com/"},
		"User-Agent": {browserUA},
	}
	api := base(n.BaseURL, "https://app.notion.com") + "/api/v3/"
	var spaces map[string]json.RawMessage
	if err := postJSON(ctx, n.HTTP, api+"getSpaces", header, map[string]any{}, &spaces); err != nil {
		return usage.Reading{}, err
	}
	space, err := notionWorkspace(spaces)
	if err != nil {
		return usage.Reading{}, err
	}
	var status struct {
		Status  string        `json:"status"`
		Resets  *number       `json:"resetsInSeconds"`
		Rolling *notionWindow `json:"window"`
		Billing *notionWindow `json:"billingPeriodWindow"`
	}
	if err := postJSON(ctx, n.HTTP, api+"getCreditRateLimitStatus", header, map[string]any{"spaceId": space}, &status); err != nil {
		return usage.Reading{}, err
	}
	if strings.EqualFold(status.Status, "not_applicable") {
		return usage.Reading{}, errors.New("accounts: notion: this workspace has no Notion AI allowance (Business and Enterprise only)")
	}
	if status.Rolling == nil && status.Billing == nil {
		return usage.Reading{}, errors.New("accounts: notion getCreditRateLimitStatus: unrecognised answer")
	}
	now := time.Now
	if n.Now != nil {
		now = n.Now
	}
	r := usage.Reading{Scope: "account"}
	if w := status.Rolling; w != nil && w.Used != nil && w.Limit != nil && w.Limit.v > 0 {
		d := notionDuration(w.Window)
		win := usage.Window{Name: windowName(d), Duration: d, UsedPct: clampPct(pct(w.Used.v, w.Limit.v))}
		if d == 0 {
			win.Name = "rolling"
		}
		if status.Resets != nil && status.Resets.ok && status.Resets.v >= 0 {
			win.ResetsAt = now().Add(time.Duration(status.Resets.v * float64(time.Second))).UTC().Truncate(time.Second)
		}
		r.Windows = append(r.Windows, win)
	}
	if w := status.Billing; w != nil && w.Used != nil && w.Limit != nil && w.Limit.v > 0 {
		r.Windows = append(r.Windows, usage.Window{Name: "month", UsedPct: clampPct(pct(w.Used.v, w.Limit.v)), ResetsAt: w.PeriodMs.t})
	}
	r.Subscription = len(r.Windows) > 0
	return r, nil
}

var notionWindowToken = regexp.MustCompile(`^([1-9][0-9]*)([mhdw])$`)

// notionDuration reads Notion's window token ("6h", "1d"); 0 when it is
// not one.
func notionDuration(token string) time.Duration {
	m := notionWindowToken.FindStringSubmatch(strings.ToLower(strings.TrimSpace(token)))
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
	return time.Duration(n) * unit
}

// notionWorkspace picks the workspace whose allowance is read from the
// getSpaces answer: the signed-in user's Business or Enterprise workspace,
// else the first by ID.
func notionWorkspace(spaces map[string]json.RawMessage) (string, error) {
	type userRecords struct {
		Users map[string]json.RawMessage `json:"notion_user"`
		Space map[string]json.RawMessage `json:"space"`
	}
	// The answer is keyed by user ID; the signed-in user is the key whose
	// own user record is inside it (or the only key).
	var user userRecords
	found := 0
	for id, raw := range spaces {
		var u userRecords
		if json.Unmarshal(raw, &u) != nil {
			continue
		}
		if _, self := u.Users[id]; self || len(spaces) == 1 {
			user, found = u, found+1
		}
	}
	if found != 1 {
		return "", errors.New("accounts: notion getSpaces: no single signed-in user in the answer")
	}
	ids := make([]string, 0, len(user.Space))
	for id := range user.Space {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return "", errors.New("accounts: notion getSpaces: no workspace")
	}
	for _, id := range ids {
		// A record is the workspace itself, or wrapped once or twice in
		// {"value": ...}.
		var rec struct {
			Tier  string `json:"subscription_tier"`
			Value *struct {
				Tier  string `json:"subscription_tier"`
				Value *struct {
					Tier string `json:"subscription_tier"`
				} `json:"value"`
			} `json:"value"`
		}
		_ = json.Unmarshal(user.Space[id], &rec)
		tier := rec.Tier
		if rec.Value != nil {
			tier = rec.Value.Tier
			if rec.Value.Value != nil {
				tier = rec.Value.Value.Tier
			}
		}
		if t := strings.ToLower(tier); t == "business" || t == "enterprise" {
			return id, nil
		}
	}
	return ids[0], nil
}
