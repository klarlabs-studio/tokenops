package findings

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/avoidable"
)

// shownClasses bounds the waste classes the weekly finding names.
const shownClasses = 3

// wasteWeek is the week's waste report as one ambient finding: what was
// avoidable in dollars and window share beside what was realized, the
// classes that carried it, and the guardrail to switch on next. It shows
// while the daemon's daily run is fresh and found anything.
func wasteWeek(s *avoidable.Snapshot, now time.Time) []Finding {
	if s == nil || now.Sub(s.ComputedAt) > avoidable.MaxSnapshotAge {
		return nil
	}
	r := s.Report
	if r.Avoidable.Tokens <= 0 || r.Avoidable.APIEquivalentUSD <= 0 {
		return nil
	}
	days := int(math.Round(r.Until.Sub(r.Since).Hours() / 24))
	title := fmt.Sprintf("Last %d days: ~%s avoidable", days, money(r.Avoidable.APIEquivalentUSD))
	var best avoidable.WindowShare
	for _, w := range r.Avoidable.WindowShare {
		if w.Pct > best.Pct {
			best = w
		}
	}
	if best.Pct > 0 {
		title += fmt.Sprintf(" (%.1f%% of a %s window)", best.Pct, best.Window)
	}
	title += fmt.Sprintf(", %s realized", money(r.Realized.USD))

	classes := append([]avoidable.ClassCost(nil), r.Classes...)
	sort.SliceStable(classes, func(i, j int) bool { return classes[i].APIEquivalentUSD > classes[j].APIEquivalentUSD })
	var parts []string
	for _, c := range classes {
		if c.APIEquivalentUSD <= 0 || len(parts) == shownClasses {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s", c.Name, money(c.APIEquivalentUSD)))
	}

	action := "`tokenops spend waste` shows each class and how it is found."
	if n := r.Next; n != nil {
		switch {
		case n.Ready && strings.HasPrefix(n.Promote, "tokenops "):
			action = fmt.Sprintf("Next guardrail: the %s, ready to %s. `%s` takes the step; nothing changes until you do.", n.Name, n.Next, n.Promote)
		case n.Ready:
			action = fmt.Sprintf("Next guardrail: the %s, ready to %s: %s.", n.Name, n.Next, n.Promote)
		default:
			action = fmt.Sprintf("Next guardrail: the %s, still gathering evidence. `tokenops spend waste` shows each class and what the %s still needs.", n.Name, n.Name)
		}
	}
	return []Finding{{Kind: KindWaste, Level: LevelInfo, Title: title, Evidence: strings.Join(parts, " · "), Action: action}}
}
