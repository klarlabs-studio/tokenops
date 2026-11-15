package agentdx

import (
	"math"
	"testing"
)

func TestContextLineFromOwnBands(t *testing.T) {
	band := func(lo, hi int64, prompts int, reject float64) ContextBand {
		return ContextBand{Lo: lo, Hi: hi, Prompts: prompts, RejectRatePct: reject}
	}
	const fallback = 600_000
	cases := []struct {
		name  string
		bands []ContextBand
		want  int64
		found bool
	}{
		{
			name:  "no bands read",
			bands: nil,
			want:  fallback,
		},
		{
			name: "quality flat across bands keeps the default line",
			bands: []ContextBand{
				band(0, 200_000, 400, 3.0), band(200_000, 400_000, 300, 3.1),
				band(400_000, 600_000, 200, 2.9), band(600_000, 800_000, 120, 3.2),
			},
			want: fallback,
		},
		{
			name: "quality falls from the 400k band on, measured on its own prompts",
			bands: []ContextBand{
				band(0, 200_000, 400, 2.0), band(200_000, 400_000, 300, 2.1),
				band(400_000, 600_000, 200, 5.4), band(600_000, 800_000, 120, 6.0),
			},
			want: 400_000, found: true,
		},
		{
			name: "a band with too few prompts is not trusted",
			bands: []ContextBand{
				band(0, 200_000, 400, 2.0), band(200_000, 400_000, 300, 2.1),
				band(400_000, 600_000, 12, 9.0),
			},
			want: fallback,
		},
		{
			name: "a rise under the margin does not move the line",
			bands: []ContextBand{
				band(0, 200_000, 400, 2.0), band(200_000, 400_000, 300, 3.5),
			},
			want: fallback,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ContextLine(tc.bands, fallback)
			if got != tc.want || ok != tc.found {
				t.Errorf("ContextLine() = %d, %v; want %d, %v", got, ok, tc.want, tc.found)
			}
		})
	}
}

// The judged reject rate pools the trusted bands only, weighting each by
// its instructions; a band too small to trust adds nothing.
func TestJudgedRejectRatePoolsTrustedBands(t *testing.T) {
	tests := []struct {
		name    string
		bands   []ContextBand
		pct     float64
		prompts int
	}{
		{"no bands", nil, 0, 0},
		{"only untrusted bands", []ContextBand{{Prompts: 10, RejectRatePct: 50}}, 0, 0},
		{"two trusted bands, weighted", []ContextBand{
			{Prompts: 100, RejectRatePct: 10}, {Prompts: 300, RejectRatePct: 20}, {Prompts: 5, RejectRatePct: 100},
		}, 17.5, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pct, n := JudgedRejectRate(tt.bands)
			if n != tt.prompts || math.Abs(pct-tt.pct) > 1e-9 {
				t.Errorf("= %v%% over %d; want %v%% over %d", pct, n, tt.pct, tt.prompts)
			}
		})
	}
}
