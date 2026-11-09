package avoidable

import (
	"math/rand/v2"
	"testing"
	"time"
)

// A window's tokens from the sorted sums are what scanning every turn
// adds up, whatever order the turns came in, with the window's start
// included and its end left out.
func TestTokenSeriesSumsAWindowAsAScanDoes(t *testing.T) {
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	r := rand.New(rand.NewPCG(1, 2))
	type turn struct {
		at     time.Time
		tokens int64
	}
	turns := make([]turn, 0, 500)
	s := &tokenSeries{}
	for range 500 {
		tr := turn{base.Add(time.Duration(r.IntN(14*24)) * time.Hour), int64(r.IntN(1000))}
		turns = append(turns, tr)
		s.at, s.tokens = append(s.at, tr.at), append(s.tokens, tr.tokens)
	}
	s.index()
	scan := func(from, to time.Time) int64 {
		var n int64
		for _, tr := range turns {
			if !tr.at.Before(from) && tr.at.Before(to) {
				n += tr.tokens
			}
		}
		return n
	}
	for range 200 {
		from := base.Add(time.Duration(r.IntN(16*24)-24) * time.Hour)
		to := from.Add(time.Duration(r.IntN(8*24)) * time.Hour)
		if got, want := s.in(from, to), scan(from, to); got != want {
			t.Fatalf("in(%v, %v) = %d, want %d", from, to, got, want)
		}
	}
	if got := s.in(base.Add(time.Hour), base); got != 0 {
		t.Errorf("a window ending before it starts = %d, want 0", got)
	}
	empty := &tokenSeries{}
	empty.index()
	if got := empty.in(base, base.Add(time.Hour)); got != 0 {
		t.Errorf("no turns = %d", got)
	}
}
