// Package attributionbridge adapts clients that can select a base URL but
// cannot attach TokenOps execution-attribution headers themselves.
package attributionbridge

import (
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

const executionHeader = "X-Tokenops-Execution-Id"

// NewHandler forwards Anthropic API requests to the local TokenOps proxy and
// adds one stable execution ID. TokenOps removes the header before upstream
// forwarding; this bridge never logs request content or credentials.
func NewHandler(target string, executionID string) (http.Handler, error) {
	targetURL, err := url.Parse(strings.TrimSpace(target))
	if err != nil || targetURL.Host == "" || (targetURL.Scheme != "http" && targetURL.Scheme != "https") {
		return nil, errors.New("attribution bridge: target must be an absolute HTTP(S) URL")
	}
	if targetURL.Hostname() != "localhost" {
		ip := net.ParseIP(targetURL.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("attribution bridge: target must be on the local machine")
		}
	}
	if targetURL.User != nil || targetURL.RawQuery != "" || targetURL.Fragment != "" || (targetURL.Path != "" && targetURL.Path != "/") {
		return nil, errors.New("attribution bridge: target must not include credentials, path, query, or fragment")
	}
	executionID = strings.TrimSpace(executionID)
	if executionID == "" || len(executionID) > 256 || strings.ContainsAny(executionID, "\r\n") {
		return nil, errors.New("attribution bridge: execution ID must contain 1 to 256 non-header characters")
	}

	reverseProxy := httputil.NewSingleHostReverseProxy(targetURL)
	director := reverseProxy.Director
	reverseProxy.Director = func(req *http.Request) {
		director(req)
		req.Header.Set(executionHeader, executionID)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/anthropic" && !strings.HasPrefix(req.URL.Path, "/anthropic/") {
			http.NotFound(w, req)
			return
		}
		reverseProxy.ServeHTTP(w, req)
	}), nil
}
