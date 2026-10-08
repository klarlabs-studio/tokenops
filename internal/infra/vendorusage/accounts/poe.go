package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerPoe registers the reader (readers_gen.go).
func readerPoe() usage.Reader { return Poe{} }

// Poe reads GET /usage/current_balance: the points left, plan and add-on
// points together (https://creator.poe.com/docs/resources/usage-api). Poe
// bills in points and publishes no rate to dollars, so the balance is kept
// in points and never converted.
//
// Best effort, it also sums the points spent over the last 30 days from GET
// /usage/points_history, as CodexBar's Poe provider does
// (Sources/CodexBarCore/Resources/Plugins/poe.js), following its cursor
// for up to poePages pages of 100. The sum is left out when the history
// cannot be read whole: a partial one would understate it. The balance is
// still read.
type Poe struct {
	BaseURL string
	HTTP    *http.Client
	// Now is the clock the 30 days end at; nil is time.Now.
	Now func() time.Time
}

// poePages bounds one reading's history: 100 entries a page.
const poePages = 20

// poeHistoryDays is the history summed.
const poeHistoryDays = 30

func (Poe) Endpoint() string               { return "poe" }
func (Poe) Provider() eventschema.Provider { return "poe" }
func (Poe) Source() string                 { return "poe-account" }

func (p Poe) Read(ctx context.Context, key string) (usage.Reading, error) {
	root := base(p.BaseURL, "https://api.poe.com") + "/usage/"
	var resp struct {
		Balance number `json:"current_point_balance"`
	}
	if err := getJSON(ctx, p.HTTP, root+"current_balance", key, &resp); err != nil {
		return usage.Reading{}, err
	}
	if !resp.Balance.ok {
		return usage.Reading{}, errors.New("accounts: poe balance: no current_point_balance")
	}
	r := usage.Reading{Scope: "account", Credits: resp.Balance.v, CreditsUnit: "points", HasCredits: true}
	if used, err := p.pointsSpent(ctx, root, key); err == nil {
		r.CreditsUsed, r.HasCreditsUsed, r.UsedPeriod = used, true, poeHistoryDays*24*time.Hour
	}
	return r, nil
}

// poeEntry is one row of the points history; CodexBar reads each field
// under the names Poe has used for it.
type poeEntry struct {
	QueryID      string          `json:"query_id"`
	CreationTime json.RawMessage `json:"creation_time"`
	Timestamp    json.RawMessage `json:"timestamp"`
	CreatedAt    json.RawMessage `json:"created_at"`
	CostPoints   *number         `json:"cost_points"`
	Points       *number         `json:"points"`
	PointCost    *number         `json:"point_cost"`
}

func (e poeEntry) at() time.Time {
	for _, raw := range []json.RawMessage{e.CreationTime, e.Timestamp, e.CreatedAt} {
		if len(raw) > 0 && string(raw) != "null" {
			return poeTime(raw)
		}
	}
	return time.Time{}
}

func (e poeEntry) points() float64 {
	for _, n := range []*number{e.CostPoints, e.Points, e.PointCost} {
		if n != nil && n.ok {
			return max(0, n.v)
		}
	}
	return 0
}

// poeTime reads a time as Unix seconds, milliseconds or microseconds, or
// an ISO string; zero when it is none of them.
func poeTime(raw json.RawMessage) time.Time {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		switch {
		case v <= 0:
			return time.Time{}
		case v > 1e14:
			return time.UnixMicro(int64(v)).UTC()
		case v > 1e12:
			return time.UnixMilli(int64(v)).UTC()
		default:
			return time.Unix(int64(v), 0).UTC()
		}
	}
	return parseTime(s)
}

// pointsSpent sums the points spent over the last 30 days, or fails when
// the history cannot be read back that far.
func (p Poe) pointsSpent(ctx context.Context, root, key string) (float64, error) {
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	cutoff := now().Add(-poeHistoryDays * 24 * time.Hour)
	total, cursor := 0.0, ""
	for range poePages {
		q := url.Values{"limit": {"100"}}
		if cursor != "" {
			q.Set("starting_after", cursor)
		}
		var page struct {
			Data       []poeEntry `json:"data"`
			Items      []poeEntry `json:"items"`
			Results    []poeEntry `json:"results"`
			NextCursor *string    `json:"next_cursor"`
			HasMore    bool       `json:"has_more"`
		}
		if err := getJSON(ctx, p.HTTP, root+"points_history?"+q.Encode(), key, &page); err != nil {
			return 0, err
		}
		rows := page.Data
		if rows == nil {
			rows = page.Items
		}
		if rows == nil {
			rows = page.Results
		}
		reachedCutoff := false
		for _, e := range rows {
			at := e.at()
			if at.IsZero() {
				continue // CodexBar skips a row it cannot date
			}
			if at.Before(cutoff) {
				reachedCutoff = true
				continue
			}
			total += e.points()
		}
		cursor = ""
		if page.NextCursor != nil {
			cursor = strings.TrimSpace(*page.NextCursor)
		}
		if cursor == "" && page.HasMore && len(rows) > 0 {
			cursor = strings.TrimSpace(rows[len(rows)-1].QueryID)
		}
		switch {
		case reachedCutoff:
			return total, nil
		case cursor == "" && page.HasMore:
			return 0, errors.New("accounts: poe points history: more pages, and no cursor to them")
		case cursor == "":
			return total, nil
		}
	}
	return 0, errors.New("accounts: poe points history: more than 30 days' entries to page through")
}
