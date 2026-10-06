// Package planevidence reads which plan the operator is on from what the
// clients already record locally, so setup can bind it instead of asking
// (autonomous by default; the operator corrects).
//
// Claude Code keeps the signed-in account's organisation type and rate
// limit tier in ~/.claude.json; Codex writes its plan_type into every
// session's rate_limits. Only those fields are read, and Claude Code's
// account email for Accounts: no token in either file is ever decoded.
package planevidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl"
)

// Evidence is what a client says about a provider's plan.
type Evidence struct {
	Provider string
	// Plan is the catalog plan the evidence names, empty when it names
	// none or several.
	Plan string
	// Candidates are the plans it could be when Plan is empty.
	Candidates []string
	// From names the client and Detail what it reported, for the
	// operator to check.
	From, Detail string
}

// All reads every client's evidence under home.
func All(home string) []Evidence {
	var out []Evidence
	if e, ok := Claude(home); ok {
		out = append(out, e)
	}
	if e, ok := Codex(filepath.Join(home, ".codex", "sessions")); ok {
		out = append(out, e)
	}
	return out
}

// claudeAccount is the part of ~/.claude.json read here.
type claudeAccount struct {
	OAuthAccount struct {
		OrganizationType          string `json:"organizationType"`
		OrganizationRateLimitTier string `json:"organizationRateLimitTier"`
		UserRateLimitTier         string `json:"userRateLimitTier"`
		SeatTier                  string `json:"seatTier"`
		EmailAddress              string `json:"emailAddress"`
	} `json:"oauthAccount"`
}

// Accounts names the account each client is signed in with, by provider,
// for the operator's own menu bar and glance: which account the windows
// are for. Claude Code records it in ~/.claude.json. Codex keeps it only
// inside its sign-in token, which is not decoded, so Codex has none.
func Accounts(home string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return out
	}
	var a claudeAccount
	if json.Unmarshal(b, &a) == nil && a.OAuthAccount.EmailAddress != "" {
		out["anthropic"] = a.OAuthAccount.EmailAddress
	}
	return out
}

// Claude reads the plan Claude Code is signed in with. ok is false when
// it is not signed in to a claude.ai account (an API key, say).
func Claude(home string) (Evidence, bool) {
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return Evidence{}, false
	}
	var a claudeAccount
	if json.Unmarshal(b, &a) != nil {
		return Evidence{}, false
	}
	oa := a.OAuthAccount
	if oa.OrganizationType == "" {
		return Evidence{}, false
	}
	tier := firstNonEmpty(oa.UserRateLimitTier, oa.OrganizationRateLimitTier)
	e := Evidence{Provider: "anthropic", From: "Claude Code",
		Detail: "account type " + oa.OrganizationType + joinIf(", rate-limit tier ", tier) + joinIf(", seat ", oa.SeatTier)}
	switch oa.OrganizationType {
	case "claude_max":
		switch {
		case strings.Contains(tier, "max_20x"):
			e.Plan = "claude-max-20x"
		case strings.Contains(tier, "max_5x"):
			e.Plan = "claude-max-5x"
		default:
			e.Candidates = []string{"claude-max-5x", "claude-max-20x"}
		}
	case "claude_pro":
		e.Plan = "claude-pro"
	case "claude_enterprise":
		e.Plan = "claude-enterprise"
	case "claude_team":
		switch {
		case strings.Contains(oa.SeatTier, "premium"):
			e.Plan = "claude-team-premium"
		case strings.Contains(oa.SeatTier, "standard"):
			e.Plan = "claude-team-standard"
		default:
			e.Candidates = []string{"claude-team-standard", "claude-team-premium"}
		}
	default:
		return Evidence{}, false
	}
	return e, true
}

// codexFilesRead bounds how many recent sessions are searched for a
// plan_type: the newest one with a token count has it.
const codexFilesRead = 5

// Codex reads the plan_type the newest Codex sessions report.
func Codex(root string) (Evidence, bool) {
	files, err := codexjsonl.FindSessionFiles(root)
	if err != nil || len(files) == 0 {
		return Evidence{}, false
	}
	sort.Slice(files, func(i, j int) bool { return modTime(files[i]) > modTime(files[j]) })
	if len(files) > codexFilesRead {
		files = files[:codexFilesRead]
	}
	for _, f := range files {
		planType := lastPlanType(f)
		if planType == "" {
			continue
		}
		e := Evidence{Provider: "openai", From: "Codex", Detail: "plan_type " + planType}
		if name, ok := plans.ForVendorPlanType("openai", planType); ok {
			e.Plan = name
		}
		return e, true
	}
	return Evidence{}, false
}

// lastPlanType is the last plan_type a session file reports.
func lastPlanType(path string) string {
	last := ""
	_ = codexjsonl.ReadFile(path, func(t codexjsonl.Turn) error {
		if t.RateLimits.PlanType != "" {
			last = t.RateLimits.PlanType
		}
		return nil
	})
	return last
}

func modTime(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().UnixNano()
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func joinIf(prefix, v string) string {
	if v == "" {
		return ""
	}
	return prefix + v
}
