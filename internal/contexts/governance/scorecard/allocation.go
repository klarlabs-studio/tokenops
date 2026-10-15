package scorecard

import (
	"fmt"
	"strings"
)

// AllocationCoverage is how much of the period's spend the operator's
// allocation rules file under a project, client or cost centre, at API
// prices. It is spend attribution's allocation side: SAC asks whether a
// call can be traced to a session, workflow or agent; this asks whether
// the spend is filed where it is billed. Reported, not graded.
type AllocationCoverage struct {
	Rules int `json:"rules"`
	// TaggedPct carries at least one tag; ByKeyPct each key.
	TaggedPct float64            `json:"tagged_pct"`
	ByKeyPct  map[string]float64 `json:"by_key_pct,omitempty"`
}

// allocationKeys orders the keys as every surface shows them.
var allocationKeys = []string{"project", "client", "cost_center"}

// writeAllocation renders the coverage under SAC.
func writeAllocation(b *strings.Builder, a *AllocationCoverage) {
	if a == nil || a.Rules == 0 {
		return
	}
	var keys []string
	for _, k := range allocationKeys {
		if v, ok := a.ByKeyPct[k]; ok && v > 0 {
			keys = append(keys, fmt.Sprintf("%s %.0f%%", k, v))
		}
	}
	line := fmt.Sprintf("  allocation tags on %.1f%% of spend", a.TaggedPct)
	if len(keys) > 0 {
		line += " (" + strings.Join(keys, ", ") + ")"
	}
	fmt.Fprintln(b, line)
}
