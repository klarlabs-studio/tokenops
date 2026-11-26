package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	cursorusage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursor"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerCursorApp registers the reader (readers_gen.go).
func readerCursorApp() usage.Reader { return CursorApp{} }

// CursorApp reads Cursor's plan from cursor.com with the sign-in Cursor's
// desktop app keeps, once the operator granted it (ADR 0013). The token is
// sent as the WorkosCursorSessionToken cookie Cursor's own client sends,
// and the same two endpoints cursor-web reads answer. Nothing is refreshed:
// an expired token is refused and the operator signs in to Cursor again.
type CursorApp struct {
	// BaseURL replaces cursor.com, for tests.
	BaseURL string
	HTTP    *http.Client
	// Now is the clock a sign-in's expiry is checked against; nil is time.Now.
	Now func() time.Time
}

func (CursorApp) Endpoint() string               { return "cursor-app" }
func (CursorApp) Provider() eventschema.Provider { return eventschema.ProviderCursor }
func (CursorApp) Source() string                 { return "cursor-app" }

// Read reads with the granted sign-in: the access token, as applogin reads it.
func (c CursorApp) Read(ctx context.Context, token string) (usage.Reading, error) {
	return c.ReadAppLogin(ctx, token)
}

// ReadAppLogin reads the plan windows with the access token.
func (c CursorApp) ReadAppLogin(ctx context.Context, token string) (usage.Reading, error) {
	now := c.now()
	cookie, err := cursorusage.SessionCookie(token, now)
	if err != nil {
		if errors.Is(err, cursorusage.ErrSignInExpired) {
			return usage.Reading{}, fmt.Errorf("%w: %w", usage.ErrAuth, err)
		}
		return usage.Reading{}, err
	}
	header := http.Header{"Cookie": {"WorkosCursorSessionToken=" + cookie}}
	root := base(c.BaseURL, "https://cursor.com")

	var summary cursorusage.Summary
	if err := doJSON(ctx, c.HTTP, http.MethodGet, root+"/api/usage-summary", header, nil, &summary); err != nil {
		return usage.Reading{}, fmt.Errorf("cursor usage-summary: %w", err)
	}
	// The weekly pool is optional: an account without one, or a failure to
	// read it, leaves the reading to usage-summary.
	var pool *cursorusage.WeeklyPool
	postHeader := http.Header{"Cookie": header["Cookie"], "Origin": {root}, "Content-Type": {"application/json"}}
	var sand cursorusage.SandStatus
	if doJSON(ctx, c.HTTP, http.MethodPost, root+"/api/dashboard/get-sand-usage-status", postHeader, []byte("{}"), &sand) == nil {
		pool = sand.Pool(now)
	}
	return cursorReading(summary, pool, now)
}

// cursorReading is the plan's windows: the weekly pool when the account is
// on one (usage-summary's monthly figures are stale for those accounts), else
// the billing cycle's share from usage-summary.
func cursorReading(s cursorusage.Summary, pool *cursorusage.WeeklyPool, now time.Time) (usage.Reading, error) {
	r := usage.Reading{Scope: "account", Subscription: true}
	if pool != nil {
		r.Windows = []usage.Window{{Name: "week", UsedPct: pool.UsedPct, Duration: 7 * 24 * time.Hour, ResetsAt: pool.ResetsAt}}
		return r, nil
	}
	read, ok := s.Read()
	if !ok {
		return usage.Reading{}, errors.New("cursor usage-summary: unrecognised answer: no plan, member or team allowance")
	}
	if !read.HasUsedPct {
		// Unlimited, or nothing to measure: no window to show.
		r.Subscription = false
		return r, nil
	}
	end, err := time.Parse(time.RFC3339, s.BillingCycleEnd)
	if err != nil || !end.After(now) {
		return usage.Reading{}, errors.New("cursor usage-summary: the billing cycle has no reset ahead")
	}
	r.Windows = []usage.Window{{Name: "month", UsedPct: read.UsedPct, Duration: 30 * 24 * time.Hour, ResetsAt: end.UTC()}}
	return r, nil
}

func (c CursorApp) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}
