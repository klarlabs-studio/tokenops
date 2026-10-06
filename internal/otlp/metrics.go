package otlp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Gauge is one derived figure as an OTLP gauge: a value per set of
// attributes, at the time it was read.
type Gauge struct {
	Name        string
	Unit        string
	Description string
	Points      []Point
}

// Point is one value of a gauge.
type Point struct {
	Attrs map[string]string
	Value float64
}

// MetricsExporter pushes gauges to an OTLP/HTTP/JSON collector's
// /v1/metrics. It carries derived figures only — shares, money, grades,
// counts — never an event, a prompt or a file.
type MetricsExporter struct {
	endpoint string
	headers  map[string]string
	resource resource
	client   *http.Client
	logger   *slog.Logger
}

// NewMetrics builds a metrics exporter from the same options as the event
// exporter; Redactor is ignored, as nothing it would redact is sent.
func NewMetrics(opts Options) (*MetricsExporter, error) {
	if opts.Endpoint == "" {
		return nil, fmt.Errorf("otlp: endpoint must not be empty")
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	svc := opts.ServiceName
	if svc == "" {
		svc = "tokenops"
	}
	res := resource{Attributes: []kv{stringKV("service.name", svc)}}
	if opts.ServiceVersion != "" {
		res.Attributes = append(res.Attributes, stringKV("service.version", opts.ServiceVersion))
	}
	return &MetricsExporter{
		endpoint: strings.TrimRight(opts.Endpoint, "/") + "/v1/metrics",
		headers:  opts.Headers, resource: res, client: opts.Client, logger: opts.Logger,
	}, nil
}

// Push sends gauges read at at. A collector that is down or refuses them
// is an error for the caller to log; the next push sends fresh figures.
func (m *MetricsExporter) Push(ctx context.Context, gauges []Gauge, at time.Time) error {
	if len(gauges) == 0 {
		return nil
	}
	ts := strconv.FormatInt(at.UnixNano(), 10)
	metrics := make([]metric, 0, len(gauges))
	for _, g := range gauges {
		points := make([]numberPoint, 0, len(g.Points))
		for _, p := range g.Points {
			v := p.Value
			points = append(points, numberPoint{Attributes: attrs(p.Attrs), TimeUnixNano: ts, AsDouble: &v})
		}
		metrics = append(metrics, metric{Name: g.Name, Unit: g.Unit, Description: g.Description, Gauge: &gauge{DataPoints: points}})
	}
	body, err := json.Marshal(exportMetricsRequest{ResourceMetrics: []resourceMetrics{{
		Resource:     m.resource,
		ScopeMetrics: []scopeMetrics{{Scope: scope{Name: "tokenops"}, Metrics: metrics}},
	}}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range m.headers {
		req.Header.Set(k, v)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("otlp metrics: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("otlp metrics: collector answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func attrs(m map[string]string) []kv {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]kv, 0, len(keys))
	for _, k := range keys {
		out = append(out, stringKV(k, m[k]))
	}
	return out
}

type exportMetricsRequest struct {
	ResourceMetrics []resourceMetrics `json:"resourceMetrics"`
}

type resourceMetrics struct {
	Resource     resource       `json:"resource"`
	ScopeMetrics []scopeMetrics `json:"scopeMetrics"`
}

type scopeMetrics struct {
	Scope   scope    `json:"scope"`
	Metrics []metric `json:"metrics"`
}

type metric struct {
	Name        string `json:"name"`
	Unit        string `json:"unit,omitempty"`
	Description string `json:"description,omitempty"`
	Gauge       *gauge `json:"gauge,omitempty"`
}

type gauge struct {
	DataPoints []numberPoint `json:"dataPoints"`
}

type numberPoint struct {
	Attributes   []kv     `json:"attributes,omitempty"`
	TimeUnixNano string   `json:"timeUnixNano"`
	AsDouble     *float64 `json:"asDouble,omitempty"`
}
