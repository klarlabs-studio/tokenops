package accounts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A gateway root configured with a query string (a key in ?key=) must not
// carry it into an error: neither in the message nor in the wrapped
// transport error, whose URL net/http keeps whole.
func TestGetGatewayErrorsOmitTheQueryString(t *testing.T) {
	const marker = "querymarker0123456789"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	var out map[string]any
	cases := map[string]string{
		"status":    srv.URL + "/v1/usage?key=" + marker,
		"refusal":   "", // filled below
		"transport": "", // a closed server
	}
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer refuse.Close()
	cases["refusal"] = refuse.URL + "/v1/usage?key=" + marker
	closed := httptest.NewServer(http.NotFoundHandler())
	cases["transport"] = closed.URL + "/v1/usage?key=" + marker
	closed.Close()
	defer srv.Close()

	for name, url := range cases {
		err := getGateway(context.Background(), nil, url, "Authorization", "Bearer k", &out)
		if err == nil {
			t.Fatalf("%s: want an error", name)
		}
		if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), "?key=") {
			t.Errorf("%s: error carries the query string: %v", name, err)
		}
		if !strings.Contains(err.Error(), "/v1/usage") {
			t.Errorf("%s: error lost the path: %v", name, err)
		}
	}
}
