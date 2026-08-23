package scorecard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

type failingReader struct{ err error }

func (f failingReader) ReadEvents(context.Context, eventschema.EventType, time.Time) ([]*eventschema.Envelope, error) {
	return nil, f.err
}

// A read that fails is not the same as a metric nobody measured, and
// rendering both as "N/A (not measured)" hides the failure completely.
// Widening the window past what the reader can load inside its deadline
// made every KPI vanish with no explanation — more data, fewer numbers.
func TestComputeSurfacesReadFailure(t *testing.T) {
	_, err := Compute(context.Background(), failingReader{err: context.DeadlineExceeded}, time.Now().Add(-time.Hour))
	if err == nil {
		t.Fatal("Compute must report a reader failure rather than returning empty KPIs")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the underlying cause preserved", err)
	}
}

// The distinction has to reach the operator, not just the caller.
func TestScorecardCarriesDegradedNote(t *testing.T) {
	sc := NewWithAgentKPIs(12, 40, 95, AgentKPIInputs{}, "")
	sc.Degraded = "timed out loading events; widen --since-days at your own risk"
	out := sc.String()
	if !strings.Contains(out, "timed out loading events") {
		t.Errorf("degraded reason must be rendered:\n%s", out)
	}
}

// A healthy scorecard says nothing extra.
func TestHealthyScorecardHasNoNote(t *testing.T) {
	sc := NewWithAgentKPIs(12, 40, 95, AgentKPIInputs{}, "")
	if strings.Contains(sc.String(), "could not be computed") {
		t.Errorf("clean run should not claim degradation:\n%s", sc.String())
	}
}
