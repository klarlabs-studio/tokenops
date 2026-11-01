package claudecodeorg

import (
	"context"
	"errors"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
)

type fake struct {
	asked []string
	fail  string
}

func (f *fake) ClaudeCodeDay(_ context.Context, day time.Time) (usage.ClaudeCodeDay, error) {
	d := day.Format("2006-01-02")
	f.asked = append(f.asked, d)
	if d == f.fail {
		return usage.ClaudeCodeDay{}, errors.New("boom")
	}
	return usage.ClaudeCodeDay{Day: d, Users: 2, Sessions: 3, Commits: 1}, nil
}

func TestReadAsksEachDayAndSums(t *testing.T) {
	f := &fake{}
	rep, err := Read(context.Background(), f, time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.asked) != 3 || f.asked[0] != "2026-10-30" || f.asked[2] != "2026-11-01" {
		t.Errorf("asked = %v", f.asked)
	}
	if rep.Since != "2026-10-30" || rep.Until != "2026-11-01" || len(rep.Days) != 3 || rep.Total.Sessions != 9 ||
		rep.Total.Users != 6 || rep.Scope == "" {
		t.Errorf("report = %+v", rep)
	}
	if _, err := Read(context.Background(), &fake{fail: "2026-10-31"}, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 3); err == nil {
		t.Error("a failed day was dropped silently")
	}
	if _, err := Read(context.Background(), f, time.Now(), MaxDays+1); err == nil {
		t.Error("range not bounded")
	}
}
