package accounts

import (
	"context"
	"testing"
)

func TestAerostackMonthlyAITokens(t *testing.T) {
	for _, tc := range []struct {
		body            string
		ok              bool
		windows, counts int
		used            float64
	}{
		{fixture(t, "aerostack"), true, 1, 0, 15},
		// Above the allowance: a grace buffer, not a block.
		{`{"tier":"free","usage":{"ai_tokens":{"used":600000,"limit":450000}}}`, true, 1, 0, 100},
		{`{"usage":{"ai_tokens":{"used":5,"limit":0}}}`, true, 0, 1, 0},
		{`{"usage":{"ai_tokens":{"used":5}}}`, true, 0, 1, 0},
		{`{"usage":{"ai_tokens":{}}}`, false, 0, 0, 0},
		{`{"usage":{"ai_tokens":{"used":1.5,"limit":10}}}`, false, 0, 0, 0},
		{`{"usage":{}}`, false, 0, 0, 0},
	} {
		srv := serve(t, "/api/billing/usage", "jwt", tc.body)
		got, err := Aerostack{BaseURL: srv.URL}.Read(context.Background(), "jwt")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (len(got.Windows) != tc.windows || len(got.Counts) != tc.counts ||
			(tc.windows == 1 && (!approx(got.Windows[0].UsedPct, tc.used) || got.LimitReached)))) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
