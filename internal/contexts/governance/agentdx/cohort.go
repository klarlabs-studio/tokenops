package agentdx

import (
	"fmt"
	"strings"
)

// Roles an instruction can play. The operator's own instructions and the
// ones a harness sends on its behalf are different workloads: an
// automatic review is a few seconds of a small model reading a diff, and
// pooling a thousand of them with substantive work makes the median
// describe neither.
const (
	// RolePrimary is an instruction the operator typed.
	RolePrimary = "primary"
	// RoleAutoReview is an automatic review the harness ran: Codex's
	// guardian reviewer, or any turn served by codex-auto-review.
	RoleAutoReview = "auto_review"
	// RoleSubagent is an instruction the harness delegated to a subagent.
	RoleSubagent = "subagent"
)

// RoleAll is the cohort value that keeps every role.
const RoleAll = "all"

// autoReviewModels are models that only ever serve automatic reviews.
var autoReviewModels = map[string]bool{"codex-auto-review": true}

// RoleOf classifies an instruction from the role its reader recorded and
// the model that served it. A model that exists only to review wins over
// an unrecorded role: older rollouts do not say what their session ran as.
func RoleOf(recorded, model string) string {
	if autoReviewModels[strings.ToLower(model)] {
		return RoleAutoReview
	}
	if recorded == "" {
		return RolePrimary
	}
	return recorded
}

// ParseRole validates a role filter. Empty and "all" keep every role.
func ParseRole(s string) (string, error) {
	r := strings.ToLower(strings.TrimSpace(s))
	r = strings.ReplaceAll(r, "-", "_")
	switch r {
	case "", RoleAll:
		return "", nil
	case RolePrimary, RoleAutoReview, RoleSubagent:
		return r, nil
	}
	return "", fmt.Errorf("role %q: want one of %s, %s, %s, %s", s, RolePrimary, RoleAutoReview, RoleSubagent, RoleAll)
}

// Cohort selects which instructions a measurement covers. The zero
// Cohort keeps everything. The harness is chosen before reading, by
// ExtractOptions.Source; the model and role need the instruction built.
type Cohort struct {
	// Model keeps instructions whose turns were mostly served by this
	// model, compared without regard to case. Empty keeps every model.
	Model string
	// Role keeps instructions of one role (RolePrimary, RoleAutoReview,
	// RoleSubagent). Empty keeps every role.
	Role string
}

// IsZero reports whether the cohort keeps everything.
func (c Cohort) IsZero() bool { return c.Model == "" && c.Role == "" }

// Match reports whether a unit belongs to the cohort.
func (c Cohort) Match(u Unit) bool {
	if c.Model != "" && !strings.EqualFold(c.Model, u.Model) {
		return false
	}
	if c.Role != "" && c.Role != u.Role {
		return false
	}
	return true
}

// FilterUnits keeps the units in the cohort.
func FilterUnits(units []Unit, c Cohort) []Unit {
	if c.IsZero() {
		return units
	}
	out := make([]Unit, 0, len(units))
	for _, u := range units {
		if c.Match(u) {
			out = append(out, u)
		}
	}
	return out
}

// FilterRecords keeps the records of the instructions in the cohort, so
// every metric computed from records describes the same instructions a
// filtered list of units would.
//
// A record belongs to the instruction of its session typed at or before
// it. Records before a session's first instruction belong to none and go;
// a compaction stays with the session when any of its instructions does,
// because compactions are counted per session.
func FilterRecords(records []Record, c Cohort) []Record {
	if c.IsZero() {
		return records
	}
	var out []Record
	for _, recs := range bySession(records) {
		sorted := sortedByTime(recs)
		units := sessionUnits(sorted)
		if len(units) == 0 {
			continue
		}
		keep := make([]bool, len(units))
		kept := false
		for i, u := range units {
			keep[i] = c.Match(u)
			kept = kept || keep[i]
		}
		if !kept {
			continue
		}
		unit := -1
		for _, r := range sorted {
			if r.Kind == KindPrompt {
				unit++
			}
			switch {
			case r.Kind == KindCompaction:
				out = append(out, r)
			case unit >= 0 && keep[unit]:
				out = append(out, r)
			}
		}
	}
	return out
}

// RoleCounts counts instructions per role, so a measurement can say what
// it pooled even when it did not filter.
func RoleCounts(units []Unit) map[string]int {
	out := map[string]int{}
	for _, u := range units {
		out[u.Role]++
	}
	return out
}
