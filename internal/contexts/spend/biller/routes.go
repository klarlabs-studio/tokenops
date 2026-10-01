package biller

import (
	"net"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Endpoint names, as recorded on each turn (attribute "endpoint").
const (
	// EndpointGateway is a host the endpoint catalog does not know.
	EndpointGateway = "gateway"
)

// EndpointName names the endpoint a base URL points at: the vendor's own
// provider name for its default or its own host, the gateway's provider
// name for a known gateway, or EndpointGateway for any other host. A
// loopback address is a local relay such as the TokenOps proxy, which
// forwards to the vendor with the harness's own login, so it counts as
// the vendor's own endpoint.
func EndpointName(baseURL, vendor string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return vendor
	}
	if e, ok := EndpointFor(baseURL); ok {
		return string(e.Provider)
	}
	if u, err := url.Parse(baseURL); err == nil {
		host := u.Hostname()
		if host == "localhost" {
			return vendor
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return vendor
		}
	}
	return EndpointGateway
}

// PlanApplies reports whether a turn billed by provider through endpoint
// is covered by that provider's plan. A plan covers only turns that went
// through the vendor's own endpoint with the plan's login. Through a
// gateway, a vendor's model runs on an API key the gateway passes on
// (FireRouter's x-anthropic-api-key), which the vendor bills per token
// whatever plan the operator holds.
func PlanApplies(provider, endpoint string) bool {
	return endpoint == "" || endpoint == provider
}

// Route is one stretch of a harness pointing at an endpoint.
type Route struct {
	Harness string    `json:"harness"`
	BaseURL string    `json:"base_url"`
	From    time.Time `json:"from"`
	// Recorded is when the route was written; From differs from it when
	// the start was dated from evidence such as a backup file.
	Recorded time.Time `json:"recorded"`
	// Evidence says how From was established: "observed" when the daemon
	// saw the setting, or the file that dated an earlier switch.
	Evidence string `json:"evidence,omitempty"`
}

// Routes is every recorded route change.
type Routes []Route

// forHarness returns the harness's routes ordered by From, later
// recordings winning ties.
func (r Routes) forHarness(harness string) []Route {
	var out []Route
	for _, x := range r {
		if x.Harness == harness {
			out = append(out, x)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].From.Equal(out[j].From) {
			return out[i].From.Before(out[j].From)
		}
		return out[i].Recorded.Before(out[j].Recorded)
	})
	return out
}

// At is the base URL the harness pointed at at t. Before the first
// recorded route, the first one applies: it is the earliest evidence
// there is, as with the plan history. ok is false when nothing is
// recorded for the harness.
func (r Routes) At(harness string, t time.Time) (string, bool) {
	rs := r.forHarness(harness)
	if len(rs) == 0 {
		return "", false
	}
	in := rs[0]
	for _, x := range rs {
		if x.From.After(t) {
			break
		}
		in = x
	}
	return in.BaseURL, true
}

// Latest is the harness's most recent route.
func (r Routes) Latest(harness string) (Route, bool) {
	rs := r.forHarness(harness)
	if len(rs) == 0 {
		return Route{}, false
	}
	return rs[len(rs)-1], true
}

// Stretch is a span of time on one base URL.
type Stretch struct {
	BaseURL  string
	From, To time.Time
}

// Stretches splits all time up to until into spans on one base URL each,
// the first reaching back to the zero time.
func (r Routes) Stretches(harness string, until time.Time) []Stretch {
	rs := r.forHarness(harness)
	if len(rs) == 0 {
		return nil
	}
	var out []Stretch
	for i, x := range rs {
		to := until
		if i+1 < len(rs) {
			to = rs[i+1].From
		}
		// The first route reaches back to the start, as At reads it.
		from := x.From
		if i == 0 {
			from = time.Time{}
		}
		if to.After(from) {
			out = append(out, Stretch{BaseURL: x.BaseURL, From: from, To: to})
		}
	}
	return out
}
