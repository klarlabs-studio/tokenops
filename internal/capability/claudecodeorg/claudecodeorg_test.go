package claudecodeorg

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/anthropic"
)

type fake struct {
	asked         []string
	fail, pending string
}

func (f *fake) ClaudeCodeDay(_ context.Context, day time.Time) (usage.ClaudeCodeDay, error) {
	d := day.Format("2006-01-02")
	f.asked = append(f.asked, d)
	switch d {
	case f.fail:
		return usage.ClaudeCodeDay{}, errors.New("boom")
	case f.pending:
		return usage.ClaudeCodeDay{}, fmt.Errorf("x: %w", usage.ErrDayNotAvailable)
	}
	return usage.ClaudeCodeDay{Day: d, Users: 2, Sessions: 3, Commits: 1}, nil
}

func TestReadAsksEachDaySumsAndKeepsPendingDays(t *testing.T) {
	f := &fake{pending: "2026-11-01"}
	rep, err := Read(context.Background(), Source{Name: "x", Scope: "s", Reporter: f}, time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.asked) != 3 || f.asked[0] != "2026-10-30" || f.asked[2] != "2026-11-01" {
		t.Errorf("asked = %v", f.asked)
	}
	if rep.Since != "2026-10-30" || rep.Until != "2026-11-01" || len(rep.Days) != 2 || rep.Total.Sessions != 6 ||
		len(rep.Pending) != 1 || rep.Pending[0] != "2026-11-01" || rep.Scope != "s" {
		t.Errorf("report = %+v", rep)
	}
	src := Source{Reporter: &fake{fail: "2026-10-31"}}
	if _, err := Read(context.Background(), src, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 3); err == nil {
		t.Error("a failed day was dropped silently")
	}
	if _, err := Read(context.Background(), Source{Reporter: f}, time.Now(), MaxDays+1); err == nil {
		t.Error("range not bounded")
	}
}

func TestSourcesFollowTheKeys(t *testing.T) {
	if s := Sources("", ""); len(s) != 0 {
		t.Errorf("sources without keys: %+v", s)
	}
	if _, err := ReadAll(context.Background(), nil, time.Now(), 1); !errors.Is(err, ErrNoKey) {
		t.Errorf("err = %v", err)
	}
	s := Sources("sk-ant-admin-x", "ak")
	if len(s) != 2 || s[0].Name != SourceConsole || s[1].Name != SourceEnterprise {
		t.Errorf("sources = %+v", s)
	}
}
