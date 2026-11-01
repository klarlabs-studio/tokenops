package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// creditServer answers like an app server for an account with banked
// reset credits: the count always, the details only when asked. It
// records, per read, whether details were asked for.
func creditServer(t *testing.T, count int, details string) (Dial, *[]bool) {
	t.Helper()
	var (
		mu    sync.Mutex
		asked []bool
	)
	return func(context.Context) (Conn, error) {
		toServer, fromClient := io.Pipe()
		toClient, fromServer := io.Pipe()
		out := make(chan string, 16)
		go func() {
			defer func() { _ = fromServer.Close() }()
			for line := range out {
				if _, err := io.WriteString(fromServer, line); err != nil {
					return
				}
			}
		}()
		go func() {
			defer close(out)
			sc := bufio.NewScanner(toServer)
			for sc.Scan() {
				var req struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
					Params struct {
						Exclude bool `json:"excludeResetCreditDetails"`
					} `json:"params"`
				}
				_ = json.Unmarshal(sc.Bytes(), &req)
				switch req.Method {
				case "initialize":
					out <- `{"id":1,"result":{}}` + "\n"
				case "account/rateLimits/read":
					mu.Lock()
					asked = append(asked, !req.Params.Exclude)
					mu.Unlock()
					credits := "null"
					if !req.Params.Exclude {
						credits = details
					}
					out <- `{"id":2,"result":{"rateLimits":{"planType":"plus","primary":{"usedPercent":40,"windowDurationMins":10080,"resetsAt":1791607287}},` +
						`"rateLimitResetCredits":{"availableCount":` + strconv.Itoa(count) + `,"credits":` + credits + `}}}` + "\n"
				}
			}
		}()
		return pipeConn{Reader: toClient, Writer: fromClient, closers: []io.Closer{fromClient, toClient, toServer}}, nil
	}, &asked
}

// The account's reset credits ride on the default bucket's reading as
// allowances. Details are asked for only once the account is known to
// have credits, at most hourly, and a read without them keeps the expiry
// last read; a redeemed credit is no longer banked.
func TestPollerReadsResetCreditsAsAllowances(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	expires := now.Add(6 * time.Hour).Unix()
	details := `[{"id":"rc-1","title":"Usage reset","grantedAt":` + strconv.FormatInt(now.Add(-24*time.Hour).Unix(), 10) +
		`,"expiresAt":` + strconv.FormatInt(expires, 10) + `,"status":"available","resetType":"codexRateLimits"},` +
		`{"id":"rc-0","grantedAt":1,"expiresAt":null,"status":"redeemed","resetType":"codexRateLimits"}]`
	dial, asked := creditServer(t, 1, details)
	clock := now
	bus := &recordingBus{}
	p := NewPoller(bus, PollerOptions{Dial: dial, Now: func() time.Time { return clock }})

	steps := []struct {
		after       time.Duration
		wantDetails bool
		wantExpiry  bool
	}{
		{0, false, false},                         // the count is not known yet
		{15 * time.Minute, true, true},            // credits known: read them in detail
		{30 * time.Minute, false, true},           // within the hour: keep the expiry read
		{time.Hour + 16*time.Minute, true, true},  // an hour on: read again
		{time.Hour + 31*time.Minute, false, true}, // and keep it
	}
	for i, s := range steps {
		clock = now.Add(s.after)
		p.Scan(context.Background())
		if got := (*asked)[i]; got != s.wantDetails {
			t.Errorf("scan %d asked for details = %v, want %v", i, got, s.wantDetails)
		}
		last := bus.got[len(bus.got)-1].Attributes
		if last["allowances_available"] != "1" {
			t.Errorf("scan %d: allowances_available = %q", i, last["allowances_available"])
		}
		hasExpiry := last["allowance_0_expires_at"] != ""
		if hasExpiry != s.wantExpiry {
			t.Errorf("scan %d: expiry kept = %v, want %v (%v)", i, hasExpiry, s.wantExpiry, last)
		}
		if last["allowance_1_kind"] != "" {
			t.Errorf("scan %d: a redeemed credit is still banked: %v", i, last)
		}
	}
	got := plans.AllowanceAttributes(1, allowances([]ResetCredit{{ID: "rc-1", GrantedAt: 5, Status: "available"}}))
	if got["allowance_0_kind"] != plans.AllowanceResetCredit || got["allowance_0_id"] != "rc-1" {
		t.Errorf("allowance attributes %v", got)
	}
}

// An account without credits is never asked for details, and its reading
// says it has none rather than nothing.
func TestPollerWithoutCreditsNeverAsksForDetails(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	dial, asked := creditServer(t, 0, "[]")
	bus := &recordingBus{}
	clock := now
	p := NewPoller(bus, PollerOptions{Dial: dial, Now: func() time.Time { return clock }})
	for i := range 3 {
		clock = now.Add(time.Duration(i) * 2 * time.Hour)
		p.Scan(context.Background())
	}
	for i, d := range *asked {
		if d {
			t.Errorf("scan %d asked for details with no credits", i)
		}
	}
	if len(bus.got) == 0 || bus.got[0].Attributes["allowances_available"] != "0" {
		t.Errorf("readings %+v", bus.got)
	}
}
