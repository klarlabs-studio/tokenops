package cursor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/cursor"
)

const sampleResponse = `{
  "gpt-4": {"numRequests": 120, "maxRequestUsage": 500},
  "gpt-4-32k": {"numRequests": 12, "maxRequestUsage": 50},
  "premiumRequests": {"numRequests": 0, "maxRequestUsage": 0},
  "startOfMonth": "2026-05-01T00:00:00.000Z"
}`

// A dollar-pool plan's usage-summary (CodexBar docs/cursor.md and its
// CursorUsageSummary model): cents, no percentage of Cursor's own.
const summaryPlan = `{
  "billingCycleStart": "2026-10-02T09:00:00.000Z",
  "billingCycleEnd": "2026-11-02T09:00:00.000Z",
  "membershipType": "pro",
  "limitType": "user",
  "isUnlimited": false,
  "individualUsage": {
    "plan": {"enabled": true, "used": 1250, "limit": 2000, "remaining": 750},
    "onDemand": {"enabled": false, "used": 0, "limit": null, "remaining": null}
  },
  "teamUsage": {}
}`

// fakeCursor serves the endpoints by path; a route left empty answers
// 404 and is recorded as hit.
type fakeCursor struct {
	summaryStatus          int
	summaryBody, sandBody  string
	legacyBody             string
	hits                   map[string]int
	sandOrigin, sandMethod string
}

func (f *fakeCursor) server(t *testing.T) *httptest.Server {
	t.Helper()
	f.hits = map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits[r.URL.Path]++
		if r.Header.Get("Cookie") != "WorkosCursorSessionToken=tok-xyz" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/usage-summary":
			if f.summaryStatus != 0 {
				w.WriteHeader(f.summaryStatus)
				return
			}
			_, _ = w.Write([]byte(f.summaryBody))
		case "/api/dashboard/get-sand-usage-status":
			f.sandOrigin, f.sandMethod = r.Header.Get("Origin"), r.Method
			if f.sandBody == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(f.sandBody))
		case "/api/usage":
			if f.legacyBody == "" || r.URL.Query().Get("user") != "user-123" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(f.legacyBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeCursor) read(t *testing.T) (*usage.UsageResponse, error) {
	t.Helper()
	c := NewClient("tok-xyz", "user-123")
	c.BaseURL = f.server(t).URL
	return c.Usage(context.Background())
}

// usage-summary is read first; the legacy request counts are not asked
// for when it answers.
func TestClientReadsUsageSummaryFirst(t *testing.T) {
	f := &fakeCursor{summaryBody: summaryPlan, legacyBody: sampleResponse}
	resp, err := f.read(t)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if resp.Summary == nil || resp.Pool != nil || len(resp.Models) != 0 {
		t.Fatalf("want the summary only; got %+v", resp)
	}
	r, ok := resp.Summary.Read()
	if !ok || r.UsedPct != 62.5 || r.Scope != usage.ScopePlan || r.LimitCents != 2000 {
		t.Errorf("reading %+v, %v", r, ok)
	}
	if f.hits["/api/usage"] != 0 {
		t.Error("the legacy endpoint was asked although usage-summary answered")
	}
	if f.sandMethod != http.MethodPost || f.sandOrigin == "" {
		t.Errorf("weekly pool asked with %s, Origin %q", f.sandMethod, f.sandOrigin)
	}
}

// The plans Cursor sells now share a weekly pool with Grok; for them
// usage-summary keeps reporting a stale monthly 100%, and the pool is
// the reading (CodexBar #4545/#4695).
func TestClientReadsTheWeeklyPool(t *testing.T) {
	reset := time.Now().Add(72 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	stale := strings.Replace(summaryPlan, `"used": 1250`, `"used": 2000, "totalPercentUsed": 100`, 1)
	f := &fakeCursor{summaryBody: stale, sandBody: `{
  "currentPeriodStart": "2026-10-15T10:00:00.000Z",
  "nextResetTimestampUtc": "` + reset + `",
  "usagePercent": 10.5,
  "hasAvailableUsage": true,
  "hasNonZeroIncludedLimit": true,
  "cursorPlanName": "Premium",
  "isUnified": true
}`}
	resp, err := f.read(t)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if resp.Pool == nil || resp.Pool.UsedPct != 10.5 || resp.Pool.ResetsAt.Format("2006-01-02T15:04:05.000Z") != reset {
		t.Fatalf("pool %+v", resp.Pool)
	}
	if !strings.HasPrefix(f.sandOrigin, "http://127.0.0.1:") {
		t.Errorf("the weekly pool was asked with Origin %q, want the site's own", f.sandOrigin)
	}
}

// A Grok Bot allowance on an older plan (isUnified false) is not the
// plan's pool, and an expired reset is no reading.
func TestClientIgnoresAPoolThatIsNotThePlan(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	for name, sand := range map[string]string{
		"not unified": `{"usagePercent": 40, "nextResetTimestampUtc": "` + future + `", "isUnified": false}`,
		"past reset":  `{"usagePercent": 40, "nextResetTimestampUtc": "` + past + `", "isUnified": true}`,
		"no share":    `{"nextResetTimestampUtc": "` + future + `", "isUnified": true}`,
	} {
		f := &fakeCursor{summaryBody: summaryPlan, sandBody: sand}
		resp, err := f.read(t)
		if err != nil || resp.Pool != nil || resp.Summary == nil {
			t.Errorf("%s: got %+v, %v", name, resp, err)
		}
	}
}

// A team member without a plan block reads the member's cap, else the
// team's pool.
func TestClientReadsATeamPool(t *testing.T) {
	f := &fakeCursor{summaryBody: `{
  "billingCycleEnd": "2026-11-02T09:00:00.000Z",
  "membershipType": "enterprise",
  "limitType": "team",
  "individualUsage": {},
  "teamUsage": {"pooled": {"enabled": true, "used": 30000, "limit": 120000}}
}`}
	resp, err := f.read(t)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if r, ok := resp.Summary.Read(); !ok || r.UsedPct != 25 || r.Scope != usage.ScopeTeam {
		t.Errorf("reading %+v, %v", r, ok)
	}
}

// The legacy request counts are read only when usage-summary is
// unavailable, and only an answer with a request allowance is a reading.
func TestClientFallsBackToLegacyRequests(t *testing.T) {
	f := &fakeCursor{summaryStatus: http.StatusNotFound, legacyBody: sampleResponse}
	resp, err := f.read(t)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if resp.Summary != nil || len(resp.Models) != 3 || resp.Models["gpt-4"].NumRequests != 120 {
		t.Fatalf("legacy %+v", resp)
	}
	if resp.StartOfMonth != "2026-05-01T00:00:00.000Z" {
		t.Errorf("startOfMonth = %q", resp.StartOfMonth)
	}

	// The vestigial answer every current plan gets: no allowance.
	vestigial := &fakeCursor{summaryStatus: http.StatusNotFound,
		legacyBody: `{"gpt-4":{"numRequests":0,"numRequestsTotal":0,"maxRequestUsage":null},"startOfMonth":"2026-10-02T09:00:00.000Z"}`}
	if resp, err := vestigial.read(t); err == nil {
		t.Errorf("a legacy answer without an allowance read as %+v", resp)
	}
}

// A refused session is an error at once, with no fallback; an answer
// TokenOps cannot read is an error with its reason, never an empty
// reading.
func TestClientRefusesWhatItCannotRead(t *testing.T) {
	refused := &fakeCursor{summaryStatus: http.StatusUnauthorized, legacyBody: sampleResponse}
	if _, err := refused.read(t); err == nil {
		t.Error("a refused session read")
	}
	if refused.hits["/api/usage"] != 0 {
		t.Error("a refused session fell back to the legacy endpoint")
	}
	for name, body := range map[string]string{
		"empty object": `{}`,
		"no limit":     `{"billingCycleEnd":"2026-11-02T09:00:00.000Z","individualUsage":{"plan":{"used":5}}}`,
		"not json":     `<html>`,
	} {
		f := &fakeCursor{summaryBody: body, legacyBody: sampleResponse}
		_, err := f.read(t)
		if err == nil || !strings.Contains(err.Error(), "usage-summary") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// Either Cookie or UserID empty → ErrMissingCredential, no HTTP call.
func TestClientUsageMissingCredentials(t *testing.T) {
	if _, err := (&Client{Cookie: "x"}).Usage(context.Background()); !errors.Is(err, usage.ErrMissingCredential) {
		t.Errorf("want ErrMissingCredential when UserID empty; got %v", err)
	}
	if _, err := (&Client{UserID: "u"}).Usage(context.Background()); !errors.Is(err, usage.ErrMissingCredential) {
		t.Errorf("want ErrMissingCredential when Cookie empty; got %v", err)
	}
}
