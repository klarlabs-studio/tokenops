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

// minBilledUSD is the least billed amount worth naming beside a plan's
// share: under a cent it reads as noise.
const minBilledUSD = 0.01

// wasteWeek is the week's waste report as one ambient finding: what was
// avoidable, in the currency the operator pays in, the classes that
// carried it, what the guardrails already saved, and the guardrail to
// switch on next. It shows while the daemon's daily run is fresh and
// found anything.
//
// On a flat-rate plan the avoidable usage cost nothing beyond the plan;
// what it took is a share of a window, so that leads and the classes are
// shares of the same window. Dollars lead only where a provider billed
// per use, and are the API price, said so, when neither is known.
func wasteWeek(s *avoidable.Snapshot, now time.Time) []Finding {
	if s == nil || now.Sub(s.ComputedAt) > avoidable.MaxSnapshotAge {
		return nil
	}
	r := s.Report
	if r.Avoidable.Tokens <= 0 || r.Avoidable.APIEquivalentUSD <= 0 {
		return nil
	}
	days := int(math.Round(r.Until.Sub(r.Since).Hours() / 24))
	share := largestShare(r.Avoidable.WindowShare)
	billed := r.Avoidable.BilledUSD >= minBilledUSD
	var title string
	var parts []string
	switch {
	case share.Pct > 0:
		title = fmt.Sprintf("Last %d days: ~%.1f%% of a %s window avoidable", days, share.Pct, share.Window)
		if billed {
			title += fmt.Sprintf(", plus %s billed per use", money(r.Avoidable.BilledUSD))
		}
		parts = classParts(r.Classes, func(c avoidable.ClassCost) (float64, string) {
			pct := shareOf(c.WindowShare, share)
			return pct, fmt.Sprintf("%.1f%%", pct)
		})
	case billed:
		title = fmt.Sprintf("Last %d days: ~%s avoidable", days, money(r.Avoidable.BilledUSD))
		parts = classParts(r.Classes, func(c avoidable.ClassCost) (float64, string) { return c.BilledUSD, money(c.BilledUSD) })
	default:
		title = fmt.Sprintf("Last %d days: ~%s avoidable at API prices", days, money(r.Avoidable.APIEquivalentUSD))
		parts = classParts(r.Classes, func(c avoidable.ClassCost) (float64, string) {
			return c.APIEquivalentUSD, money(c.APIEquivalentUSD)
		})
	}
	evidence := strings.Join(parts, " · ")
	if r.Realized.USD >= minBilledUSD {
		evidence = strings.TrimPrefix(evidence+". Guardrails already saved ~"+money(r.Realized.USD)+" at API prices", ". ")
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
	return []Finding{{Key: key(KindWaste, "week"), Kind: KindWaste, Level: LevelInfo, Title: title, Evidence: evidence, Action: action}}
}

// largestShare is the window the amount took most of.
func largestShare(ss []avoidable.WindowShare) avoidable.WindowShare {
	var best avoidable.WindowShare
	for _, w := range ss {
		if w.Pct > best.Pct {
			best = w
		}
	}
	return best
}

// shareOf is the share of the same provider's window in ss.
func shareOf(ss []avoidable.WindowShare, of avoidable.WindowShare) float64 {
	for _, w := range ss {
		if w.Provider == of.Provider && w.Window == of.Window {
			return w.Pct
		}
	}
	return 0
}

// classParts names the classes that carried the most, by measure, each
// with its figure.
func classParts(classes []avoidable.ClassCost, measure func(avoidable.ClassCost) (float64, string)) []string {
	sorted := append([]avoidable.ClassCost(nil), classes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, _ := measure(sorted[i])
		b, _ := measure(sorted[j])
		return a > b
	})
	var parts []string
	for _, c := range sorted {
		v, words := measure(c)
		if v <= 0 || len(parts) == shownClasses {
			break
		}
		parts = append(parts, c.Name+" "+words)
	}
	return parts
}
