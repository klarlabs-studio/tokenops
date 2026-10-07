package dashauth

import "testing"

func newTestAuth(t *testing.T) *Authenticator {
	t.Helper()
	a, err := New(Config{AdminToken: "secret-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

func TestNewRejectsMissingToken(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error when no API token is set")
	}
}

func TestAuthorizeHeaderRequiresBearerToken(t *testing.T) {
	a := newTestAuth(t)
	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{name: "missing", header: "", want: false},
		{name: "wrong", header: "Bearer invalid", want: false},
		{name: "no scheme", header: "secret-token", want: false},
		{name: "other scheme", header: "Basic secret-token", want: false},
		{name: "lowercase scheme", header: "bearer secret-token", want: false},
		{name: "prefix of token", header: "Bearer secret", want: false},
		{name: "token with suffix", header: "Bearer secret-token2", want: false},
		{name: "valid", header: "Bearer secret-token", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := a.AuthorizeHeader(tc.header); got != tc.want {
				t.Fatalf("AuthorizeHeader(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}
