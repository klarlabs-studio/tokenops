package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerAbacus registers the reader (readers_gen.go).
func readerAbacus() usage.Reader { return Abacus{} }

// Abacus reads Abacus AI's (ChatLLM / RouteLLM) compute credits used this
// billing month from apps.abacus.ai with its browser session, as CodexBar's
// Abacus plugin does (Sources/CodexBarCore/Resources/Plugins/abacus.ts):
// the credit total and the credits left (required) and the next billing
// date (best-effort), which the month window resets at.
type Abacus struct {
	BaseURL string
	HTTP    *http.Client
}

func (Abacus) Endpoint() string               { return "abacus" }
func (Abacus) Provider() eventschema.Provider { return "abacus" }
func (Abacus) Source() string                 { return "abacus-web" }

var abacusRefusal = regexp.MustCompile(`expired|session|login|authenticate|unauthorized|unauthenticated|forbidden`)

func (a Abacus) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (Abacus AI is read with the apps.abacus.ai Cookie header)", usage.ErrAuth)
	}
	root := base(a.BaseURL, "https://apps.abacus.ai")
	header := http.Header{"Cookie": {cookie}, "Accept": {"application/json"}, "Content-Type": {"application/json"}}
	call := func(method, path string, body []byte) (map[string]any, error) {
		data, err := doWeb(ctx, a.HTTP, method, root+path, header, body)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Success bool           `json:"success"`
			Result  map[string]any `json:"result"`
			Error   string         `json:"error"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("accounts: %s %s: %w", method, hostPath(root+path), err)
		}
		if !resp.Success || resp.Result == nil {
			if abacusRefusal.MatchString(strings.ToLower(resp.Error)) {
				return nil, fmt.Errorf("%w (%s refused the session)", usage.ErrAuth, hostPath(root+path))
			}
			return nil, fmt.Errorf("accounts: %s %s: not successful", method, hostPath(root+path))
		}
		return resp.Result, nil
	}
	points, err := call(http.MethodGet, "/api/_getOrganizationComputePoints", nil)
	if err != nil {
		return usage.Reading{}, err
	}
	total, okTotal := points["totalComputePoints"].(float64)
	left, okLeft := points["computePointsLeft"].(float64)
	if !okTotal || !okLeft {
		return usage.Reading{}, errors.New("accounts: abacus: no credit fields in the answer")
	}
	w := usage.Window{Name: "month"}
	if total > 0 {
		w.UsedPct = clampPct(pct(total-left, total))
	}
	if billing, err := call(http.MethodPost, "/api/_getBillingInfo", []byte("{}")); err == nil {
		if next := timeOf(billing["nextBillingDate"]); !next.IsZero() {
			w.ResetsAt, w.Duration = next, next.Sub(next.AddDate(0, -1, 0))
		}
	}
	return usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{w}}, nil
}
