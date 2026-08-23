package daemon

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Before any reading is taken the probe must say "unknown", not zero. A
// rule gated on window pressure that saw a default of 0% would never fire;
// one that saw 0 as trustworthy would fire on a shortage nobody measured.
func TestProbeUnknownBeforeFirstReading(t *testing.T) {
	p := newWindowProbe()
	if _, ok := p.Pct(eventschema.ProviderAnthropic); ok {
		t.Error("an unread probe must not report a trustworthy value")
	}
}

// A fresh reading is usable.
func TestProbeReportsFreshReading(t *testing.T) {
	p := newWindowProbe()
	p.set(eventschema.ProviderAnthropic, 42.5)
	pct, ok := p.Pct(eventschema.ProviderAnthropic)
	if !ok {
		t.Fatal("fresh reading should be trustworthy")
	}
	if pct != 42.5 {
		t.Errorf("pct = %v, want 42.5", pct)
	}
}

// A reading nobody refreshed is worse than none: the window it describes
// has since reset or filled. Past maxWindowAge the probe goes quiet, and
// rules gated on it stop firing rather than acting on stale pressure.
func TestProbeExpiresStaleReading(t *testing.T) {
	p := newWindowProbe()
	p.readings[eventschema.ProviderAnthropic] = windowReading{
		pct: 90, at: time.Now().Add(-2 * maxWindowAge),
	}
	if _, ok := p.Pct(eventschema.ProviderAnthropic); ok {
		t.Error("a stale reading must not be reported as trustworthy")
	}
}

// Providers are tracked independently.
func TestProbeIsPerProvider(t *testing.T) {
	p := newWindowProbe()
	p.set(eventschema.ProviderAnthropic, 80)
	if _, ok := p.Pct(eventschema.ProviderOpenAI); ok {
		t.Error("a reading for one provider must not answer for another")
	}
}
