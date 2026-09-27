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

const (
	executionHeader = "X-Tokenops-Execution-Id"
	workflowHeader  = "X-Tokenops-Workflow-Id"
)

// NewHandler forwards Anthropic API requests to the local TokenOps proxy and
// adds stable execution and optional workflow IDs. TokenOps removes the
// headers before upstream forwarding; this bridge never logs request content
// or credentials.
func NewHandler(target, executionID, workflowID string) (http.Handler, error) {
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
	workflowID = strings.TrimSpace(workflowID)
	if !validHeaderID(executionID, false) {
		return nil, errors.New("attribution bridge: execution ID must contain 1 to 256 non-header characters")
	}
	if !validHeaderID(workflowID, true) {
		return nil, errors.New("attribution bridge: workflow ID must contain at most 256 non-header characters")
	}

	reverseProxy := httputil.NewSingleHostReverseProxy(targetURL)
	director := reverseProxy.Director
	reverseProxy.Director = func(req *http.Request) {
		director(req)
		req.Header.Set(executionHeader, executionID)
		if workflowID != "" {
			req.Header.Set(workflowHeader, workflowID)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/anthropic" && !strings.HasPrefix(req.URL.Path, "/anthropic/") {
			http.NotFound(w, req)
			return
		}
		reverseProxy.ServeHTTP(w, req)
	}), nil
}

func validHeaderID(id string, optional bool) bool {
	if id == "" {
		return optional
	}
	return len(id) <= 256 && !strings.ContainsAny(id, "\r\n")
}
