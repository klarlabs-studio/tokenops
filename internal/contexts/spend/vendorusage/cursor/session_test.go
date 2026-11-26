package cursor

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeJWT is an unsigned token with the given claims, shaped like the
// access token Cursor keeps in its app database.
func fakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return header + "." + enc.EncodeToString(body) + ".sig"
}

func TestSessionCookie(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour).Unix()
	past := now.Add(-time.Hour).Unix()
	tests := []struct {
		name    string
		token   func(t *testing.T) string
		want    string // the cookie's user ID, "" when an error is expected
		wantErr error
	}{
		{name: "bare subject", token: func(t *testing.T) string {
			return fakeJWT(t, map[string]any{"sub": "user_01ABC", "exp": future})
		}, want: "user_01ABC"},
		{name: "provider prefix is dropped", token: func(t *testing.T) string {
			return fakeJWT(t, map[string]any{"sub": "auth0|user_01ABC", "exp": future})
		}, want: "user_01ABC"},
		{name: "numeric provider subject", token: func(t *testing.T) string {
			return fakeJWT(t, map[string]any{"sub": "google-oauth2|103547991", "exp": future})
		}, want: "103547991"},
		{name: "not a JWT", token: func(t *testing.T) string { return "opaque-token" }, wantErr: ErrNotJWT},
		{name: "payload not base64", token: func(t *testing.T) string { return "h.@@@.s" }, wantErr: ErrNotJWT},
		{name: "payload not JSON", token: func(t *testing.T) string {
			return "h." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".s"
		}, wantErr: ErrNotJWT},
		{name: "subject missing", token: func(t *testing.T) string {
			return fakeJWT(t, map[string]any{"exp": future})
		}, wantErr: ErrNoUserID},
		{name: "subject with a provider prefix and no user", token: func(t *testing.T) string {
			return fakeJWT(t, map[string]any{"sub": "auth0|", "exp": future})
		}, wantErr: ErrNoUserID},
		{name: "subject with characters a cookie cannot carry", token: func(t *testing.T) string {
			return fakeJWT(t, map[string]any{"sub": "auth0|user 01;x", "exp": future})
		}, wantErr: ErrNoUserID},
		{name: "expiry missing", token: func(t *testing.T) string {
			return fakeJWT(t, map[string]any{"sub": "user_01ABC"})
		}, wantErr: ErrNoExpiry},
		{name: "expired", token: func(t *testing.T) string {
			return fakeJWT(t, map[string]any{"sub": "user_01ABC", "exp": past})
		}, wantErr: ErrSignInExpired},
		{name: "expires within the minute", token: func(t *testing.T) string {
			return fakeJWT(t, map[string]any{"sub": "user_01ABC", "exp": now.Add(30 * time.Second).Unix()})
		}, wantErr: ErrSignInExpired},
		{name: "encrypted at rest", token: func(t *testing.T) string {
			return "cursor-safe-storage:v1:" + base64.StdEncoding.EncodeToString([]byte("v10xxxx"))
		}, wantErr: ErrEncrypted},
		{name: "token that would break the header", token: func(t *testing.T) string {
			return fakeJWT(t, map[string]any{"sub": "user_01ABC", "exp": future}) + "; Path=/"
		}, wantErr: ErrNotJWT},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := tt.token(t)
			got, err := SessionCookie(token, now)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("SessionCookie() error = %v, want %v", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), token) {
					t.Fatalf("error carries the token: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("SessionCookie() error = %v", err)
			}
			want := tt.want + "%3A%3A" + token
			if got != want {
				t.Fatalf("SessionCookie() = %q, want %q", got, want)
			}
		})
	}
}
