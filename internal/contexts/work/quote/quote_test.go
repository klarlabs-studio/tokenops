package quote

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

// tasks are n synthetic past instructions with the given costs, a day
// apart, ending yesterday.
func tasks(client, model, effort, kind string, usd ...float64) []Sample {
	out := make([]Sample, 0, len(usd))
	for i, v := range usd {
		out = append(out, Sample{
			Client: client, Provider: "anthropic", Model: model, Effort: effort, Kind: kind,
			At: now.Add(-time.Duration(i+1) * time.Hour), USD: v, Tokens: int64(v * 1000),
		})
	}
	return out
}

// spread is n costs 1, 2, …, n: p50 and p90 are easy to read off.
func spread(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = float64(i + 1)
	}
	return out
}

func TestBuildPicksTheMostSpecificBasisWithEnoughHistory(t *testing.T) {
	edits := tasks("claude-code", "claude-opus-4-5", "high", "edit", spread(20)...)
	other := tasks("claude-code", "claude-opus-4-5", "low", "lookup", spread(30)...)
	tests := []struct {
		name      string
		history   []Sample
		want      Want
		basis     Basis
		samples   int
		p50, p90  float64
		quoted    bool
		noteMatch string
	}{
		{name: "same model, effort and kind",
			history: append(append([]Sample{}, edits...), other...),
			want:    Want{Client: "claude-code", Model: "claude-opus-4-5", Effort: "high", Kind: "edit"},
			basis:   BasisModelEffortKind, samples: 20, p50: 10.5, p90: 18.1, quoted: true},
		{name: "kind known, effort unknown: same model and kind",
			history: append(append([]Sample{}, edits...), other...),
			want:    Want{Model: "claude-opus-4-5", Kind: "edit"},
			basis:   BasisModelKind, samples: 20, p50: 10.5, p90: 18.1, quoted: true},
		{name: "too few of the kind falls back to the model and effort",
			history: append(tasks("claude-code", "claude-opus-4-5", "low", "edit", 1, 2, 3), other...),
			want:    Want{Model: "claude-opus-4-5", Effort: "low", Kind: "edit"},
			basis:   BasisModelEffort, samples: 33, quoted: true},
		{name: "too little history gives no number",
			history: tasks("claude-code", "claude-opus-4-5", "high", "edit", 1, 2, 3),
			want:    Want{Model: "claude-opus-4-5", Effort: "high", Kind: "edit"},
			samples: 3, quoted: false, noteMatch: "3 past"},
		{name: "another model's history never stands in",
			history: other,
			want:    Want{Model: "claude-sonnet-4-5", Kind: "lookup"},
			quoted:  false, noteMatch: "0 past"},
		{name: "another client's history is left out when the client is named",
			history: tasks("codex", "claude-opus-4-5", "high", "edit", spread(25)...),
			want:    Want{Client: "claude-code", Model: "claude-opus-4-5", Kind: "edit"},
			quoted:  false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := Build(tc.history, tc.want, nil, now)
			if q.Quoted() != tc.quoted {
				t.Fatalf("quoted = %v, want %v (%+v)", q.Quoted(), tc.quoted, q)
			}
			if !tc.quoted {
				if q.P50USD != 0 || q.P90USD != 0 {
					t.Errorf("an unquoted task carries a number: %+v", q)
				}
				if tc.noteMatch != "" && !strings.Contains(q.Summary, tc.noteMatch) {
					t.Errorf("summary %q does not say %q", q.Summary, tc.noteMatch)
				}
				return
			}
			if q.Basis != tc.basis || q.Samples != tc.samples {
				t.Errorf("basis = %s over %d, want %s over %d", q.Basis, q.Samples, tc.basis, tc.samples)
			}
			if tc.p50 != 0 && (q.P50USD != tc.p50 || q.P90USD != tc.p90) {
				t.Errorf("p50/p90 = %v/%v, want %v/%v", q.P50USD, q.P90USD, tc.p50, tc.p90)
			}
			if q.P90Tokens < q.P50Tokens || q.P50Tokens == 0 {
				t.Errorf("tokens p50/p90 = %d/%d", q.P50Tokens, q.P90Tokens)
			}
			if !strings.Contains(q.Summary, "estimate") {
				t.Errorf("summary %q is not labelled an estimate", q.Summary)
			}
		})
	}
}

// Instructions older than the lookback, or from the future, do not count.
func TestBuildReadsOnlyTheLookback(t *testing.T) {
	recent := tasks("claude-code", "m", "", "edit", spread(10)...)
	old := tasks("claude-code", "m", "", "edit", spread(30)...)
	for i := range old {
		old[i].At = now.Add(-Lookback - time.Hour)
	}
	q := Build(append(recent, old...), Want{Model: "m", Kind: "edit"}, nil, now)
	if q.Quoted() || q.Samples != 10 {
		t.Fatalf("quote = %+v, want unquoted on 10 recent samples", q)
	}
}

// A model id with a date suffix is the same model as its alias.
func TestBuildMatchesAModelWithItsDatedID(t *testing.T) {
	h := tasks("claude-code", "claude-opus-4-5-20251101", "", "edit", spread(20)...)
	q := Build(h, Want{Model: "Claude-Opus-4-5", Kind: "edit"}, nil, now)
	if !q.Quoted() {
		t.Fatalf("quote = %+v, want quoted", q)
	}
}

func TestWindowSharesFollowTheCalibratedRate(t *testing.T) {
	h := tasks("claude-code", "m", "", "edit", spread(20)...)
	windows := []Window{
		{Name: "5h", Duration: 5 * time.Hour, UsedPct: 70, ResetsIn: 2 * time.Hour, USDPerPct: 2, CalibratedOn: 6},
		{Name: "week", Duration: 7 * 24 * time.Hour, UsedPct: 99, USDPerPct: 10, CalibratedOn: 3},
		{Name: "week (Fable)", Duration: 7 * 24 * time.Hour, UsedPct: 10},
	}
	q := Build(h, Want{Model: "m", Kind: "edit"}, windows, now)
	if len(q.Windows) != 3 {
		t.Fatalf("windows = %+v", q.Windows)
	}
	five := q.Windows[0]
	if !five.Known || five.P50Pct != 5.3 || five.P90Pct != 9.1 || five.LeftPct != 30 || five.LeftAfterP90Pct != 21 || !five.FitsP90 {
		t.Errorf("5h share = %+v", five)
	}
	week := q.Windows[1]
	if !week.Known || week.P90Pct != 1.8 || week.FitsP90 || week.LeftAfterP90Pct != 0 || !strings.Contains(week.Note, "more than the 1% left") {
		t.Errorf("week share = %+v", week)
	}
	fable := q.Windows[2]
	if fable.Known || fable.P90Pct != 0 || !strings.Contains(fable.Note, "no measured rate") {
		t.Errorf("an uncalibrated window carries a share: %+v", fable)
	}
}

// A share that does not fit what is left says so; one that does not fit
// is never reported as fitting.
func TestWindowShareThatDoesNotFit(t *testing.T) {
	h := tasks("claude-code", "m", "", "edit", spread(20)...)
	q := Build(h, Want{Model: "m", Kind: "edit"}, []Window{{Name: "5h", UsedPct: 95, USDPerPct: 1, CalibratedOn: 5}}, now)
	w := q.Windows[0]
	if w.FitsP90 || w.LeftAfterP90Pct != 0 || !strings.Contains(w.Note, "more than the 5% left") {
		t.Errorf("window = %+v", w)
	}
}

func TestUnquotedTaskHasNoWindowShare(t *testing.T) {
	q := Build(nil, Want{Model: "m"}, []Window{{Name: "5h", UsedPct: 10, USDPerPct: 1, CalibratedOn: 5}}, now)
	if len(q.Windows) != 1 || q.Windows[0].Known || q.Windows[0].LeftPct != 90 {
		t.Errorf("windows = %+v", q.Windows)
	}
}

func TestCalibrate(t *testing.T) {
	scored := func(within, over int) []Scored {
		var out []Scored
		for range within {
			out = append(out, Scored{P90: 10, Actual: 8})
		}
		for range over {
			out = append(out, Scored{P90: 10, Actual: 12})
		}
		return out
	}
	tests := []struct {
		name   string
		scored []Scored
		enough bool
		pct    float64
	}{
		{name: "none", scored: nil},
		{name: "too few to show", scored: scored(5, 1), pct: 83.3},
		{name: "enough", scored: scored(9, 1), enough: true, pct: 90},
		{name: "a p90 that is too low shows", scored: scored(6, 6), enough: true, pct: 50},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Calibrate(tc.scored)
			if c.Enough != tc.enough || c.Scored != len(tc.scored) || c.WithinP90Pct != tc.pct {
				t.Errorf("calibration = %+v, want enough %v at %v%%", c, tc.enough, tc.pct)
			}
			if c.MinScored != MinScored || c.Note == "" {
				t.Errorf("calibration = %+v", c)
			}
		})
	}
}

func TestCalibrationNoteSaysWhenTheP90RunsLow(t *testing.T) {
	var s []Scored
	for i := range 12 {
		s = append(s, Scored{P90: 10, Actual: float64(5 + i)})
	}
	c := Calibrate(s)
	if !strings.Contains(c.Note, "runs low") {
		t.Errorf("note = %q", c.Note)
	}
}
