package daemon

import (
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/teamshare"
)

func TestTeamCadence(t *testing.T) {
	day := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	failed := errors.New("server unreachable")
	// Built and not sent, because part of it could not be read.
	unsent := &teamshare.IncompleteError{Unread: []string{"event store: context deadline exceeded"}}
	type upload struct {
		at   time.Time
		err  error
		want int
	}
	tests := []struct {
		name    string
		n       int
		uploads []upload
	}{
		{"the first upload after a start is full", 14, []upload{{day, nil, 14}}},
		{"later ones that day cover two days", 14, []upload{{day, nil, 14}, {day.Add(time.Hour), nil, 2}, {day.Add(2 * time.Hour), nil, 2}}},
		{"the first of the next UTC day is full", 14, []upload{{day, nil, 14}, {day.Add(14 * time.Hour), nil, 14}, {day.Add(15 * time.Hour), nil, 2}}},
		{"a failed full one is retried full", 14, []upload{{day, failed, 14}, {day.Add(time.Hour), nil, 14}, {day.Add(2 * time.Hour), nil, 2}}},
		{"after a failed recent one the next is full", 14, []upload{{day, nil, 14}, {day.Add(time.Hour), failed, 2}, {day.Add(2 * time.Hour), nil, 14}}},
		{"an unsent recent one is covered again by the next recent one", 14, []upload{{day, nil, 14}, {day.Add(time.Hour), unsent, 2}, {day.Add(2 * time.Hour), nil, 2}}},
		{"an unsent full one is retried full", 14, []upload{{day, unsent, 14}, {day.Add(time.Hour), nil, 14}, {day.Add(2 * time.Hour), nil, 2}}},
		{"an unsent one does not clear a failure before it", 14, []upload{{day, nil, 14}, {day.Add(time.Hour), failed, 2}, {day.Add(2 * time.Hour), unsent, 14}, {day.Add(3 * time.Hour), nil, 14}}},
		{"a window of two days or less is always full", 2, []upload{{day, nil, 2}, {day.Add(time.Hour), nil, 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c teamCadence
			for i, u := range tt.uploads {
				got := c.days(u.at, tt.n)
				if got != u.want {
					t.Fatalf("upload %d covers %d days, want %d", i, got, u.want)
				}
				c.done(u.at, got, tt.n, u.err)
			}
		})
	}
}
