package otlp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func sampleGauges() []Gauge {
	return []Gauge{{Name: "tokenops.plan.window.utilization", Unit: "%", Description: "share used",
		Points: []Point{{Attrs: map[string]string{"provider": "openai", "window": "week"}, Value: 75}}}}
}

// Gauges go to /v1/metrics as OTLP JSON, with the configured headers.
func TestPushSendsOTLPJSON(t *testing.T) {
	var body map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/metrics" || r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
	}))
	defer srv.Close()
	m, err := NewMetrics(Options{Endpoint: srv.URL + "/", Headers: map[string]string{"Authorization": "Bearer t"}, ServiceVersion: "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Push(context.Background(), sampleGauges(), time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer t" {
		t.Errorf("auth header %q", auth)
	}
	raw, _ := json.Marshal(body)
	for _, want := range []string{`"service.name"`, `"tokenops"`, `"service.version"`, `"tokenops.plan.window.utilization"`,
		`"gauge"`, `"asDouble":75`, `"timeUnixNano":"1700000000000000000"`, `"key":"provider"`, `"stringValue":"openai"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("payload lacks %s:\n%s", want, raw)
		}
	}
}

// A collector that refuses the push is an error the caller logs.
func TestPushReportsRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()
	m, _ := NewMetrics(Options{Endpoint: srv.URL})
	if err := m.Push(context.Background(), sampleGauges(), time.Now()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("refusal = %v", err)
	}
	if err := m.Push(context.Background(), nil, time.Now()); err != nil {
		t.Errorf("an empty push failed: %v", err)
	}
	if _, err := NewMetrics(Options{}); err == nil {
		t.Error("no endpoint accepted")
	}
}

// TestPushToALiveCollector pushes to a real OpenTelemetry Collector when
// OTLP_LIVE_ENDPOINT names one.
func TestPushToALiveCollector(t *testing.T) {
	ep := os.Getenv("OTLP_LIVE_ENDPOINT")
	if ep == "" {
		t.Skip("set OTLP_LIVE_ENDPOINT to a collector's OTLP/HTTP base URL")
	}
	m, err := NewMetrics(Options{Endpoint: ep, ServiceVersion: "live-test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Push(context.Background(), sampleGauges(), time.Now()); err != nil {
		t.Fatal(err)
	}
}
