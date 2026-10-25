package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

func TestWriteTipVolumeShowsPerDayAndUnseenRate(t *testing.T) {
	vols := []coachcap.KindVolume{
		{Kind: "quota_burn", Offers: 8, PerDay: []int{1, 0, 2, 1, 1, 2, 1}, Unseen: 6, Followed: 1, Ignored: 1},
		{Kind: "compact_now", Offers: 2, PerDay: []int{0, 0, 0, 1, 0, 0, 1}, Followed: 2},
	}
	stats := coachhook.Stats{StoodDown: map[string]int{"quota_100": 14}}
	var b bytes.Buffer
	writeTipVolume(&b, vols, stats)
	out := b.String()
	for _, want := range []string{
		"quota_burn", "8 tips", "[1 0 2 1 1 2 1]", "75% unseen",
		"compact_now", "unseen rate n/a",
		"stood down quota_100", "14 times", "Claude Code's own limit message",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	var empty bytes.Buffer
	writeTipVolume(&empty, nil, coachhook.Stats{})
	if empty.Len() != 0 {
		t.Errorf("printed for an empty ledger: %q", empty.String())
	}
}

// The cross-plan reading asks every plan but the session's own.
func TestOtherPlansSkipsTheSessionsOwn(t *testing.T) {
	var asked []eventschema.Provider
	plan := func(_ context.Context, p eventschema.Provider, _ time.Time) planReading {
		asked = append(asked, p)
		if p == eventschema.ProviderOpenAI {
			return planReading{Quota: &coachhook.Quota{Provider: "openai"}}
		}
		return planReading{}
	}
	got := otherPlans(context.Background(), plan, eventschema.ProviderAnthropic, time.Now())
	if len(got) != 1 || got[0].Provider != "openai" || len(asked) != 1 || asked[0] != eventschema.ProviderOpenAI {
		t.Fatalf("got %+v, asked %v", got, asked)
	}
}
