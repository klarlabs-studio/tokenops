package proxy

import "net/http"

// BearerVerifier decides whether an Authorization header value carries the
// API credential. dashauth.Authenticator satisfies it; the decision stays in
// the security domain and this package only applies it to HTTP.
type BearerVerifier interface {
	AuthorizeHeader(authorization string) bool
}

// BearerAuth adapts a BearerVerifier to the DashAuth middleware contract:
// a request whose Authorization header the verifier rejects is answered
// 401 with a Bearer challenge and never reaches next.
func BearerAuth(v BearerVerifier) DashAuth {
	return bearerAuth{verifier: v}
}

type bearerAuth struct {
	verifier BearerVerifier
}

// Middleware rejects requests without a valid Authorization bearer token.
func (b bearerAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !b.verifier.AuthorizeHeader(r.Header.Get("Authorization")) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="tokenops"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
