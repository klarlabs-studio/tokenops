package headroom_test

import (
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Every surface reads headroom from the same inputs. The CLI's `plan
// headroom` left out the plans TokenOps bound on a client's word, `otel`
// left out the plan history (so a plan switch could read as an early
// reset), the MCP server's own fallback left out the signed-in accounts,
// and the team upload the observed limits. LocalDeps is the one builder.
func TestLocalDepsCarriesEveryInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := &config.Config{}
	var r eventsReader
	price := func(*eventschema.PromptEvent, time.Time) (float64, error) { return 0, nil }
	d := headroom.LocalDeps(cfg, &r, price)
	switch {
	case d.Config != cfg, d.Reader == nil, d.Price == nil:
		t.Errorf("config, reader or price not passed through: %+v", d)
	case d.Accounts == nil, d.PlanFollows == nil, d.PlanHistory == nil, d.Observed == nil:
		t.Errorf("a machine-local input is missing: %+v", d)
	}
	if d.AdviceCache != nil {
		t.Error("the advice cache is the daemon's to keep, not every caller's")
	}
	if got := d.PlanFollows(map[string]string{}, time.Now()); len(got) != 0 {
		t.Errorf("plan follows in an empty home: %v", got)
	}
}

// A nil reader stays a nil reader, which the capability answers as storage
// disabled, and a nil price stays nil.
func TestLocalDepsKeepsNilsNil(t *testing.T) {
	d := headroom.LocalDeps(&config.Config{}, nil, nil)
	if d.Reader != nil || d.Price != nil {
		t.Errorf("nil inputs became non-nil: %+v", d)
	}
}
