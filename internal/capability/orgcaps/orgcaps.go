// Package orgcaps reads the spend limits a vendor sets on each member of an
// organisation, for the administrator who runs TokenOps' team plane: the
// vendor's cap is the ceiling any team rule sits under (gap G7).
//
// It runs on the administrator's machine with their own vendor key. The
// vendor's members are matched to the team's by e-mail address, in memory:
// nothing read here is uploaded, kept or logged.
//
// Claude Enterprise is the one source today (the Spend Limits API).
// OpenAI's per-seat credit limits are set only in its billing settings,
// with no API to read them.
package orgcaps

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	anthropicapi "go.klarlabs.de/tokenops/internal/infra/vendorusage/anthropic"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// Reader reads a vendor's limits and the requests for more.
type Reader interface {
	SpendLimits(ctx context.Context) ([]anthropicapi.SpendLimit, error)
	PendingIncreaseRequests(ctx context.Context) ([]anthropicapi.IncreaseRequest, error)
}

// ErrNoKey says no Claude Enterprise key is configured.
var ErrNoKey = errors.New("no Claude Enterprise key: set vendor_usage.anthropic.analytics_key (TOKENOPS_ANTHROPIC_ANALYTICS_KEY), " +
	"a key the primary owner creates in claude.ai > Organization settings > API with the read:spend_limits scope")

// NearPct is the share of a cap from which a member is flagged as near it.
const NearPct = 90

// Row is one member's cap for one period.
type Row struct {
	// Member is the team member's name when the e-mail matched one, else
	// the vendor's name for them; MemberID is the team's id, when matched.
	Member   string `json:"member"`
	MemberID string `json:"member_id,omitempty"`
	Email    string `json:"email,omitempty"`
	Vendor   string `json:"vendor"`
	Period   string `json:"period"`
	Currency string `json:"currency"`
	// Limit is in the currency's major unit; nil is no limit, 0 is the
	// plan's included usage only.
	Limit   *float64 `json:"limit"`
	Spent   float64  `json:"spent"`
	UsedPct *float64 `json:"used_pct,omitempty"`
	// Source says where the cap was inherited from: "user", "seat tier
	// enterprise_standard", "group rbac_group_01…", "organization".
	Source string `json:"source"`
	// Pending is a request for a higher cap waiting for an admin, and
	// PendingSince when it was made.
	Pending      bool      `json:"pending,omitempty"`
	PendingSince time.Time `json:"pending_since,omitzero"`
}

// Near reports whether the member has used NearPct of the cap or more.
func (r Row) Near() bool { return r.UsedPct != nil && *r.UsedPct >= NearPct }

// Report is the organisation's caps beside the team.
type Report struct {
	Vendor string `json:"vendor"`
	Rows   []Row  `json:"rows"`
	// NotInTeam counts the vendor's members whose address no team member
	// has; NoSeat names the team members the vendor has no row for.
	NotInTeam int      `json:"not_in_team"`
	NoSeat    []string `json:"no_seat,omitempty"`
	Pending   int      `json:"pending"`
}

// Read reads the vendor's caps and matches them to members. members may
// be empty: the caps are then shown by the vendor's names alone.
func Read(ctx context.Context, r Reader, members []teamwire.Member) (Report, error) {
	limits, err := r.SpendLimits(ctx)
	if err != nil {
		if errors.Is(err, anthropicapi.ErrSpendLimitsScope) {
			return Report{}, fmt.Errorf("%w: create a key with read:spend_limits in claude.ai > Organization settings > API "+
				"(the primary owner can add it beside read:analytics on one key): %w", anthropicapi.ErrSpendLimitsScope, err)
		}
		return Report{}, err
	}
	pending, err := r.PendingIncreaseRequests(ctx)
	if err != nil {
		return Report{}, err
	}
	return assemble(limits, pending, members), nil
}

func assemble(limits []anthropicapi.SpendLimit, pending []anthropicapi.IncreaseRequest, members []teamwire.Member) Report {
	byEmail := map[string]teamwire.Member{}
	for _, m := range members {
		if m.Email != "" {
			byEmail[strings.ToLower(m.Email)] = m
		}
	}
	waiting := map[string]anthropicapi.IncreaseRequest{}
	for _, p := range pending {
		waiting[p.UserID+"\x00"+p.Period] = p
	}
	rep := Report{Vendor: "Claude", Pending: len(pending)}
	seen := map[string]bool{}
	counted := map[string]bool{}
	for _, l := range limits {
		unit := minorPerMajor(l.Currency)
		row := Row{Member: cmp.Or(l.Name, l.Email, l.UserID), Email: l.Email, Vendor: rep.Vendor, Period: l.Period,
			Currency: l.Currency, Spent: l.Spent / unit, Source: sourceWords(l.Source, l.SourceID)}
		if m, ok := byEmail[strings.ToLower(l.Email)]; ok && l.Email != "" {
			row.Member, row.MemberID = m.Name, m.ID
			seen[m.ID] = true
		} else if !counted[l.UserID] {
			counted[l.UserID] = true
			rep.NotInTeam++
		}
		if l.Limit != nil {
			limit := *l.Limit / unit
			row.Limit = &limit
			if limit > 0 {
				pct := 100 * row.Spent / limit
				row.UsedPct = &pct
			}
		}
		if p, ok := waiting[l.UserID+"\x00"+l.Period]; ok {
			row.Pending, row.PendingSince = true, p.CreatedAt
		}
		rep.Rows = append(rep.Rows, row)
	}
	for _, m := range members {
		if !seen[m.ID] {
			rep.NoSeat = append(rep.NoSeat, m.Name)
		}
	}
	slices.SortStableFunc(rep.Rows, byUrgency)
	return rep
}

// byUrgency puts a waiting request first, then the fullest cap; no cap
// sorts last.
func byUrgency(a, b Row) int {
	if a.Pending != b.Pending {
		if a.Pending {
			return -1
		}
		return 1
	}
	pa, pb := -1.0, -1.0
	if a.UsedPct != nil {
		pa = *a.UsedPct
	}
	if b.UsedPct != nil {
		pb = *b.UsedPct
	}
	switch {
	case pa > pb:
		return -1
	case pa < pb:
		return 1
	}
	return strings.Compare(a.Member, b.Member)
}

// sourceWords names where a cap was inherited from.
func sourceWords(kind, id string) string {
	switch kind {
	case "seat_tier":
		return strings.TrimSpace("seat tier " + id)
	case "rbac_group":
		return strings.TrimSpace("group " + id)
	case "user":
		return "set for them"
	case "":
		return "unknown"
	}
	return strings.TrimSpace(strings.ReplaceAll(kind, "_", " ") + " " + id)
}

// zeroDecimal are the ISO 4217 currencies with no minor unit, which
// Anthropic's minor-unit amounts are already whole units of.
var zeroDecimal = []string{"JPY", "KRW", "CLP", "ISK", "VND", "TWD", "HUF", "UGX"}

// minorPerMajor is how many minor units make one of the currency.
func minorPerMajor(currency string) float64 {
	if slices.Contains(zeroDecimal, strings.ToUpper(currency)) {
		return 1
	}
	return 100
}
