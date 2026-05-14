package daemon

import "testing"

// sanitizeInstance trims trailing .local and whitespace from the OS
// hostname so the Bonjour instance name reads cleanly in service
// browsers. Empty input must fall back to "tokenops" so the advertise
// never registers an empty label.
func TestSanitizeInstance(t *testing.T) {
	cases := []struct{ in, want string }{
		{"laptop", "laptop"},
		{"laptop.local", "laptop"},
		{" laptop ", "laptop"},
		{"", "tokenops"},
	}
	for _, c := range cases {
		if got := sanitizeInstance(c.in); got != c.want {
			t.Errorf("sanitizeInstance(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

// startMDNSAdvertise must surface a typed parse error when the addr
// is malformed so daemon.Run can log + fall back to the loopback URL
// instead of crashing. The actual zeroconf registration is exercised
// in an integration test (gated by an env var so CI without multicast
// doesn't flake).
func TestStartMDNSAdvertiseBadAddr(t *testing.T) {
	closer, url, err := startMDNSAdvertise("not-a-host-port", false)
	if err == nil {
		t.Fatalf("expected error on bad addr")
	}
	if url != "" {
		t.Errorf("URL should be empty on error; got %q", url)
	}
	closer() // must be safe to call even after failure
}
