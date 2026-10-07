package state

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

func TestSourceHealthOf(t *testing.T) {
	tests := []struct {
		name      string
		in        []SourceReport
		wantLen   int
		unhealthy int
	}{
		{"nil becomes empty", nil, 0, 0},
		{"healthy only", []SourceReport{{Tag: "a", State: freshness.StateHealthy}}, 1, 0},
		{"one failing", []SourceReport{
			{Tag: "a", State: freshness.StateHealthy},
			{Tag: "b", State: freshness.StateFailing, Severity: freshness.SeverityDegraded},
		}, 2, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SourceHealthOf(tt.in)
			if got.Sources == nil {
				t.Fatal("Sources is nil; want an empty list")
			}
			if len(got.Sources) != tt.wantLen || got.Unhealthy != tt.unhealthy {
				t.Errorf("got %d sources, %d unhealthy; want %d, %d", len(got.Sources), got.Unhealthy, tt.wantLen, tt.unhealthy)
			}
		})
	}
}
