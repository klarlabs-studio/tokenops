package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSnapshotRedactsOTelHeaders(t *testing.T) {
	c := Config{
		Listen: "127.0.0.1:7878",
		OTel: OTelConfig{
			Enabled:  true,
			Endpoint: "https://collector.example.com",
			Headers: map[string]string{
				"X-Tenant-Token": "secret-bearer-abc123",
				"Authorization":  "Bearer raw-secret-key",
			},
		},
	}
	data, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "secret-bearer-abc123") || strings.Contains(s, "raw-secret-key") {
		t.Fatalf("snapshot leaked header value: %s", s)
	}
	if !strings.Contains(s, SensitiveHeaderPlaceholder) {
		t.Errorf("snapshot missing placeholder: %s", s)
	}
	// Header names preserved.
	if !strings.Contains(s, "X-Tenant-Token") {
		t.Errorf("header name dropped: %s", s)
	}
}

func TestSnapshotPreservesOriginal(t *testing.T) {
	c := Config{OTel: OTelConfig{Headers: map[string]string{"k": "secret"}}}
	if _, err := c.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if c.OTel.Headers["k"] != "secret" {
		t.Errorf("snapshot mutated input: %v", c.OTel.Headers)
	}
}

func TestSnapshotEmptyHeadersStable(t *testing.T) {
	c := Config{Listen: "x"}
	data, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
}
