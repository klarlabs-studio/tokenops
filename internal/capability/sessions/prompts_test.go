package sessions

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/prompts"
)

func TestWithoutTextKeepsCountsAndDropsQuotes(t *testing.T) {
	f := withoutText(prompts.Findings{
		TotalPrompts:      9,
		VagueShortSamples: []string{"fix it"},
		RegenerateSamples: []string{"again"},
		RepeatedPrompts:   []prompts.RepeatedItem{{Text: "run the tests", Count: 4}},
		Recommendations:   []prompts.Recommendation{{ID: "vague", Evidence: []string{"fix it"}, Frequency: 3, Before: "fix all"}},
	})
	if f.TotalPrompts != 9 || f.VagueShortSamples != nil || f.RegenerateSamples != nil {
		t.Errorf("findings %+v", f)
	}
	if len(f.RepeatedPrompts) != 1 || f.RepeatedPrompts[0].Text != "" || f.RepeatedPrompts[0].Count != 4 {
		t.Errorf("repeated %+v", f.RepeatedPrompts)
	}
	if r := f.Recommendations[0]; r.Evidence != nil || r.Frequency != 3 || r.Before != "fix all" {
		t.Errorf("recommendation %+v", r)
	}
}
