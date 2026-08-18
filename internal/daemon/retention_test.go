package daemon

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestRetentionPolicies(t *testing.T) {
	pols, err := retentionPolicies(config.RetentionConfig{
		Keep: map[string]string{"prompt": "30d", "workflow": "0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pols) != 1 {
		t.Fatalf("got %d policies; want 1 (zero window skipped)", len(pols))
	}
	if pols[0].EventType != eventschema.EventTypePrompt || pols[0].KeepFor != 30*24*time.Hour {
		t.Fatalf("policy = %+v", pols[0])
	}
}

func TestRetentionPoliciesRejectUnknown(t *testing.T) {
	if _, err := retentionPolicies(config.RetentionConfig{Keep: map[string]string{"nope": "1h"}}); err == nil {
		t.Fatal("expected error")
	}
}
