package state

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
)

// Soft problems keep a ready process ready but degraded; the outdated
// binary leads the warnings and the missing daemon comes before quiet
// sources, which are its symptom.
func TestComputeStatusOrdersProblems(t *testing.T) {
	st := ComputeStatus(StatusInputs{
		Ready:         true,
		Stale:         []string{"claude-code-jsonl is quiet"},
		DaemonMissing: &Problem{Warning: "no daemon", NextAction: "start it"},
		Drift:         &Problem{Warning: "old binary", NextAction: "reconnect"},
	})
	if st.State != Degraded || !st.Ready {
		t.Fatalf("state %s ready %v", st.State, st.Ready)
	}
	want := []string{"old binary", "no daemon", "claude-code-jsonl is quiet"}
	if strings.Join(st.Warnings, "|") != strings.Join(want, "|") {
		t.Errorf("warnings %q, want %q", st.Warnings, want)
	}
	if st.NextActions[len(st.NextActions)-1] != "reconnect" {
		t.Errorf("next actions %q", st.NextActions)
	}
}

func TestComputeStatusStates(t *testing.T) {
	if st := ComputeStatus(StatusInputs{Ready: true}); st.State != Ready || len(st.Warnings) != 0 {
		t.Errorf("healthy = %+v", st)
	}
	if st := ComputeStatus(StatusInputs{}); st.State != NotReady {
		t.Errorf("not ready = %+v", st)
	}
	if st := ComputeStatus(StatusInputs{Ready: true, Dropped: 3}); st.State != Degraded || len(st.Warnings) != 1 {
		t.Errorf("lost writes = %+v", st)
	}
}

func TestVendorUsageCountsEverySource(t *testing.T) {
	cfg := config.Default()
	count := func(context.Context, time.Time, time.Time) (map[string]int64, error) {
		return map[string]int64{"claude-code-statusline": 4}, nil
	}
	got, err := VendorUsageOf(context.Background(), &cfg, count, 0, time.Now())
	if err != nil || got.WindowHours != 24 || len(got.Sources) != len(cfg.VendorUsageSources()) {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, s := range got.Sources {
		if s.SourceTag == "claude-code-statusline" && (s.EventsInWin != 4 || !s.Enabled) {
			t.Errorf("status line source %+v", s)
		}
	}
	if none, _ := VendorUsageOf(context.Background(), nil, count, 0, time.Now()); none.Error != ErrStorageDisabled {
		t.Errorf("no config = %+v", none)
	}
}

func TestDataSourcesSaysWhenHealthIsUnknown(t *testing.T) {
	count := func(context.Context, time.Time, time.Time) (map[string]int64, error) {
		return map[string]int64{"codex-jsonl": 2}, nil
	}
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	got, err := DataSourcesOf(context.Background(), count, nil, since, time.Time{})
	if err != nil || got.Counts["codex-jsonl"] != 2 || got.FreshnessUnavailable == "" || got.Window.Since != "2026-10-01T00:00:00Z" {
		t.Fatalf("got %+v, %v", got, err)
	}
	healthy, _ := DataSourcesOf(context.Background(), count, []freshness.Report{}, since, time.Time{})
	if healthy.FreshnessUnavailable != "" {
		t.Errorf("known health reported as unknown: %+v", healthy)
	}
}

// A refused reader degrades status with its own next action. Only quiet
// sources, lost writes or a missing daemon add the daemon remedy: sending
// an operator to restart a healthy daemon over a revoked key is the advice
// this replaces.
func TestComputeStatusFailingSources(t *testing.T) {
	st := ComputeStatus(StatusInputs{Ready: true, Failing: []string{"cursor-web is refused"}})
	if st.State != Degraded || len(st.Warnings) != 1 {
		t.Fatalf("status = %+v", st)
	}
	if !contains(st.NextActions, FailingSourceNextAction) || contains(st.NextActions, config.StaleIngestionNextAction) {
		t.Errorf("next actions %q", st.NextActions)
	}
	st = ComputeStatus(StatusInputs{Ready: true, Stale: []string{"quiet"}, Failing: []string{"refused"}})
	if strings.Join(st.Warnings, "|") != "refused|quiet" {
		t.Errorf("warnings %q, want the refused source first", st.Warnings)
	}
	if !contains(st.NextActions, FailingSourceNextAction) || !contains(st.NextActions, config.StaleIngestionNextAction) {
		t.Errorf("next actions %q", st.NextActions)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// The daemon grades its status from the readers' health when it has it,
// so a reader that polled successfully and found nothing stays quiet even
// though the event-count check would call it stale.
func TestStatusOfPrefersReaderHealth(t *testing.T) {
	d := Deps{
		Ready: func() bool { return true },
		Health: func() []freshness.Report {
			return []freshness.Report{{Tag: "gemini-cli", State: freshness.StateIdle, Severity: freshness.SeverityOK}}
		},
		Stale: func(context.Context, time.Time) []config.StaleSource {
			return []config.StaleSource{{SourceTag: "gemini-cli", WindowHours: 48}}
		},
	}
	if st := StatusOf(context.Background(), d, time.Now()); st.State != Ready || len(st.Warnings) != 0 {
		t.Errorf("status = %+v, want ready with no warnings", st)
	}
	d.Health = nil
	if st := StatusOf(context.Background(), d, time.Now()); len(st.Warnings) != 1 {
		t.Errorf("without reader health the count check applies; got %+v", st)
	}
}
