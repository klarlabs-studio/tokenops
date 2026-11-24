package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerOpenDesign registers the reader (readers_gen.go).
func readerOpenDesign() usage.Reader { return OpenDesign{} }

// OpenDesign reads the active workspace's Coding Plan windows the
// open-design.ai dashboard shows, ported from CodexBar
// (Sources/CodexBarCore/Resources/Plugins/opendesign.js,
// Providers/OpenDesign, docs/opendesign.md): GET /api/v1/workspaces/current
// and /api/v1/workspaces/{id}/billing/coding-plan-usage on
// amr-api.open-design.ai, with the browser session. Inference keys cannot
// read billing. The endpoints are undocumented.
//
// The 18,000-second window is the "5h" window and the 604,800-second one
// the "week"; credits used against the window's limit is the share used.
type OpenDesign struct {
	BaseURL string
	HTTP    *http.Client
}

func (OpenDesign) Endpoint() string               { return "opendesign" }
func (OpenDesign) Provider() eventschema.Provider { return "opendesign" }
func (OpenDesign) Source() string                 { return "opendesign-web" }

var (
	errOpenDesignShape = errors.New("accounts: opendesign coding plan usage: unrecognised answer")
	openDesignID       = lazyRegexp(`^[A-Za-z0-9_-]{1,128}$`)
)

func (o OpenDesign) Read(ctx context.Context, key string) (usage.Reading, error) {
	cookie := cookieHeader(key)
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (the OpenDesign credential is not a Cookie header)", usage.ErrAuth)
	}
	root := base(o.BaseURL, "https://amr-api.open-design.ai") + "/api/v1/workspaces/"
	get := func(path, workspace string, out any) error {
		h := http.Header{"Cookie": {cookie}, "Accept": {"application/json"}}
		if workspace != "" {
			h.Set("X-Vela-Workspace-Id", workspace)
		}
		body, err := doWeb(ctx, o.HTTP, http.MethodGet, root+path, h, nil)
		if err != nil {
			return err
		}
		if json.Unmarshal(body, out) != nil {
			return errOpenDesignShape
		}
		return nil
	}
	var ws struct {
		ID string `json:"workspaceId"`
	}
	if err := get("current", "", &ws); err != nil {
		return usage.Reading{}, err
	}
	if !openDesignID().MatchString(ws.ID) {
		return usage.Reading{}, errOpenDesignShape
	}
	var resp struct {
		WorkspaceID string `json:"workspaceId"`
		Eligible    *bool  `json:"eligible"`
		Windows     *[]struct {
			Duration json.RawMessage `json:"durationSeconds"`
			Used     json.RawMessage `json:"usedCredits"`
			Limit    json.RawMessage `json:"limitCredits"`
			ResetsAt string          `json:"resetsAt"`
		} `json:"windows"`
	}
	if err := get(ws.ID+"/billing/coding-plan-usage", ws.ID, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.WorkspaceID != ws.ID || resp.Eligible == nil || resp.Windows == nil {
		return usage.Reading{}, errOpenDesignShape
	}
	if !*resp.Eligible {
		return usage.Reading{}, errors.New("accounts: opendesign: this workspace's subscription has no Coding Plan usage")
	}
	r := usage.Reading{Scope: "workspace", Subscription: true}
	seen := map[time.Duration]bool{}
	for _, w := range *resp.Windows {
		var secs int64
		if json.Unmarshal(w.Duration, &secs) != nil {
			continue
		}
		d := time.Duration(secs) * time.Second
		if d != 5*time.Hour && d != 7*24*time.Hour {
			continue // an unrecognised window is not read as one of these
		}
		if seen[d] {
			return usage.Reading{}, errOpenDesignShape
		}
		seen[d] = true
		used, ok1 := openDesignCredits(w.Used)
		limit, ok2 := openDesignCredits(w.Limit)
		if !ok1 || !ok2 || limit <= 0 {
			return usage.Reading{}, errOpenDesignShape
		}
		r.Windows = append(r.Windows, usage.Window{Name: windowName(d), UsedPct: clampPct(pct(used, limit)),
			Duration: d, ResetsAt: parseTime(w.ResetsAt)})
	}
	if len(r.Windows) == 0 {
		return usage.Reading{}, errors.New("accounts: opendesign: no Coding Plan window in the answer")
	}
	return r, nil
}

// openDesignCredits reads a non-negative number or numeric string.
func openDesignCredits(raw json.RawMessage) (float64, bool) {
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return f, f >= 0
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil && v >= 0 && s != "" && s[0] != '+' && s[0] != '-'
}
