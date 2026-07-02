package formatter

import (
	"strings"
	"testing"
)

const curlRaw = `* Trying 93.184.216.34:443...
* Connected to api.example.com (93.184.216.34) port 443
* ALPN: curl offers h2,http/1.1
* TLSv1.3 (OUT), TLS handshake, ClientHello (1):
* TLSv1.3 (IN), TLS handshake, ServerHello (2):
* subject: CN=api.example.com
* start date: Jan  1 00:00:00 2026 GMT
* using HTTP/2
* Server certificate:
> GET /users HTTP/2
> Host: api.example.com
> User-Agent: curl/8.4.0
> Accept: */*
< HTTP/2 200
< content-type: application/json
< content-length: 45
{"users":[{"id":1,"name":"alice"}]}
`

const curlFailRaw = `* Trying 203.0.113.5:443...
* Could not resolve host: nonexistent.invalid
curl: (6) Could not resolve host: nonexistent.invalid
`

func TestCurl_CriticalSurvivesEveryLevel(t *testing.T) {
	c := NewCurl()
	cases := []struct {
		name     string
		raw      string
		critical []string
	}{
		{
			name:     "success",
			raw:      curlRaw,
			critical: []string{"< HTTP/2 200"},
		},
		{
			name:     "failure",
			raw:      curlFailRaw,
			critical: []string{"curl: (6) Could not resolve host: nonexistent.invalid"},
		},
	}
	for _, tc := range cases {
		for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
			res, ok := c.Format([]byte(tc.raw), level)
			if !ok {
				t.Fatalf("%s level=%s ok=false", tc.name, level)
			}
			if !res.CriticalKept {
				t.Fatalf("%s level=%s CriticalKept=false", tc.name, level)
			}
			compact := string(res.Compact)
			for _, cr := range tc.critical {
				if !strings.Contains(compact, strings.TrimSpace(cr)) {
					t.Errorf("%s level=%s dropped critical %q\ngot:\n%s", tc.name, level, cr, compact)
				}
			}
		}
	}
}

func TestCurl_BalancedDropsTrace(t *testing.T) {
	c := NewCurl()
	res, _ := c.Format([]byte(curlRaw), LossBalanced)
	compact := string(res.Compact)
	for _, noise := range []string{"* Trying", "* TLSv", "> GET"} {
		if strings.Contains(compact, noise) {
			t.Errorf("balanced kept trace noise %q:\n%s", noise, compact)
		}
	}
	if !strings.Contains(compact, "< HTTP/2 200") {
		t.Errorf("balanced dropped the response status:\n%s", compact)
	}
	if !strings.Contains(compact, `{"users":[{"id":1,"name":"alice"}]}`) {
		t.Errorf("balanced dropped the response body:\n%s", compact)
	}
}

func TestCurl_AggressiveDropsResponseHeaders(t *testing.T) {
	c := NewCurl()
	res, _ := c.Format([]byte(curlRaw), LossAggressive)
	compact := string(res.Compact)
	for _, header := range []string{"< content-type:", "< content-length:"} {
		if strings.Contains(compact, header) {
			t.Errorf("aggressive kept non-status response header %q:\n%s", header, compact)
		}
	}
	if !strings.Contains(compact, "< HTTP/2 200") {
		t.Errorf("aggressive dropped the response status:\n%s", compact)
	}
	if !strings.Contains(compact, `{"users":[{"id":1,"name":"alice"}]}`) {
		t.Errorf("aggressive dropped the response body:\n%s", compact)
	}
}

func TestCurl_MonotonicReduction(t *testing.T) {
	c := NewCurl()
	var last = 1 << 30
	for _, level := range []LossLevel{LossConservative, LossBalanced, LossAggressive} {
		res, _ := c.Format([]byte(curlRaw), level)
		if res.BytesAfter > last {
			t.Errorf("level=%s grew: %d > %d", level, res.BytesAfter, last)
		}
		last = res.BytesAfter
	}
}

func TestCurl_NonCurlFallsBackToGeneric(t *testing.T) {
	c := NewCurl()
	raw := "just some random log output\nno http here at all\n"
	res, ok := c.Format([]byte(raw), LossBalanced)
	if !ok {
		t.Fatal("ok=false")
	}
	if !strings.Contains(res.Notes, "generic") {
		t.Errorf("expected generic fallback, got %q", res.Notes)
	}
}
