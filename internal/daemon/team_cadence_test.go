package daemon

import (
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/teamshare"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
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

// An upload whose inputs have not moved is not built again (ADR 0019):
// overnight, with nobody working, each hourly upload rebuilt the same two
// days for six to nine minutes and sent nothing new. Only what the event
// store holds is compared, so an upload is still built at least every
// teamQuietMax for what it reads elsewhere (transcripts, git, a
// provider's cost API).
func TestTeamCadenceSkipsAnUnchangedStore(t *testing.T) {
	day := time.Date(2026, 11, 13, 2, 0, 0, 0, time.UTC)
	at := sqlite.Position{Rowid: 700, Seq: 40}
	moved := sqlite.Position{Rowid: 701, Seq: 40}
	changed := sqlite.Position{Rowid: 700, Seq: 41}
	failed := errors.New("server unreachable")
	unsent := &teamshare.IncompleteError{Unread: []string{"transcripts: deadline"}}
	type upload struct {
		at   time.Time
		pos  sqlite.Position
		err  error
		want bool // built and sent, rather than skipped
	}
	tests := []struct {
		name    string
		uploads []upload
	}{
		{"the first upload is built", []upload{{day, at, nil, true}}},
		{"an unchanged store skips the next", []upload{{day, at, nil, true}, {day.Add(time.Hour), at, nil, false}, {day.Add(2 * time.Hour), at, nil, false}}},
		{"a new event builds it", []upload{{day, at, nil, true}, {day.Add(time.Hour), moved, nil, true}}},
		{"an updated or deleted event builds it", []upload{{day, at, nil, true}, {day.Add(time.Hour), changed, nil, true}}},
		{"a skip does not hold back the next change", []upload{{day, at, nil, true}, {day.Add(time.Hour), at, nil, false}, {day.Add(2 * time.Hour), moved, nil, true}}},
		{"it is built again after teamQuietMax", []upload{{day, at, nil, true}, {day.Add(teamQuietMax - time.Minute), at, nil, false}, {day.Add(teamQuietMax), at, nil, true}}},
		{"the next UTC day builds it", []upload{{day.Add(21 * time.Hour), at, nil, true}, {day.Add(22 * time.Hour), at, nil, true}}},
		{"a failed upload is not skipped", []upload{{day, at, failed, true}, {day.Add(time.Hour), at, nil, true}, {day.Add(2 * time.Hour), at, nil, false}}},
		{"an unsent upload is not skipped", []upload{{day, at, nil, true}, {day.Add(time.Hour), moved, unsent, true}, {day.Add(2 * time.Hour), moved, nil, true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c teamCadence
			for i, u := range tt.uploads {
				days := c.days(u.at, 14)
				if built := !c.quiet(u.at, days, u.pos); built != u.want {
					t.Fatalf("upload %d built = %v, want %v", i, built, u.want)
				}
				if u.want {
					c.done(u.at, days, 14, u.err)
					c.sent(u.at, days, u.pos, u.err)
				}
			}
		})
	}
}
