package accounts

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

// readerDoubaoCLI registers the reader (readers_gen.go): `arkcli usage plan
// --format json`, run as its descriptor says (cliSource), parsed as
// CodexBar's Doubao provider parses it.
func readerDoubaoCLI() usage.Reader {
	return cliSource{provider: "doubao", tag: "doubao-cli", parse: parseArkcliPlan}
}

// arkcliPlanOrder is the plans read, in order: the Coding Plan's windows
// when there is one, as the Volcengine reader does, else the Agent Plan's.
var arkcliPlanOrder = []string{"coding-plan", "coding-plan-team", "agent-plan", "agent-plan-team"}

// parseArkcliPlan reads arkcli's plan usage: per product, its periods'
// percentages (already 0–100) and resets.
func parseArkcliPlan(out string) (usage.Reading, error) {
	var resp struct {
		Viewer *struct {
			AuthMethod string `json:"auth_method"`
		} `json:"viewer"`
		Items []struct {
			Product    string `json:"product"`
			Subscribed *bool  `json:"subscribed"`
			Periods    []struct {
				Label   string `json:"label"`
				Percent number `json:"percent"`
				ResetAt stamp  `json:"reset_at"`
			} `json:"periods"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(jsonPart(out)), &resp); err != nil {
		return usage.Reading{}, fmt.Errorf("%w: not arkcli's JSON", errNoUsage)
	}
	if resp.Viewer != nil && strings.EqualFold(resp.Viewer.AuthMethod, "none") {
		return usage.Reading{}, cliAuthError("arkcli")
	}
	lengths := map[string]time.Duration{
		"5h": 5 * time.Hour, "session": 5 * time.Hour, "5-hour": 5 * time.Hour, "five_hour": 5 * time.Hour,
		"weekly": 7 * 24 * time.Hour, "week": 7 * 24 * time.Hour,
		"monthly": 30 * 24 * time.Hour, "month": 30 * 24 * time.Hour,
	}
	for _, product := range arkcliPlanOrder {
		for _, item := range resp.Items {
			if item.Product != product || (item.Subscribed != nil && !*item.Subscribed) {
				continue
			}
			r := usage.Reading{Scope: "account"}
			for _, p := range item.Periods {
				d, ok := lengths[strings.ToLower(strings.TrimSpace(p.Label))]
				if !ok || !p.Percent.ok {
					continue
				}
				r.Windows = append(r.Windows, cliWindow(d, p.Percent.v, p.ResetAt.t))
			}
			if len(r.Windows) > 0 {
				r.Subscription = true
				return r, nil
			}
		}
	}
	return usage.Reading{}, fmt.Errorf("%w: no subscribed Coding or Agent Plan", errNoUsage)
}

// jsonPart is output from its first "{": a CLI may print a notice first.
func jsonPart(out string) string {
	if i := strings.IndexByte(out, '{'); i > 0 {
		return out[i:]
	}
	return out
}
