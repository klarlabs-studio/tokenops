package coach

import (
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// The periods Activities reports.
const (
	// PeriodToday is since local midnight.
	PeriodToday = "today"
	// PeriodWeek is the last ActivityDays days.
	PeriodWeek = "7d"
)

// ActivityDays is the look-back of PeriodWeek.
const ActivityDays = 7

// Activity is what the coach did on its own over one period, counted
// from its own ledgers: the follow-through ledger's moves and the read
// guard's refusals. Advice it gave and proposals it put to the operator
// are not in it; follow_through says what became of those.
type Activity struct {
	Period string    `json:"period"`
	Since  time.Time `json:"since"`
	// SubagentMoves counts subagents moved to a cheaper model;
	// DefaultModelMoves the times new sessions were started a tier down.
	SubagentMoves     int `json:"subagent_moves"`
	DefaultModelMoves int `json:"default_model_moves"`
	// RefusedRereads counts the re-reads of unchanged files the read
	// guard refused, RefusedTokens their estimated size.
	RefusedRereads int   `json:"refused_rereads"`
	RefusedTokens  int64 `json:"refused_tokens"`
	// Summary words it in one line ("moved 3 subagents to a cheaper
	// model · refused 258 re-reads"); empty when the coach did nothing.
	Summary string `json:"summary,omitempty"`
}

// RefusedRereads counts the read guard's refusals from since on; a
// variable so tests do not read the machine's ledger.
var RefusedRereads = func(since time.Time) (int, int64) {
	s, err := readguard.ReadStatsSince("", since)
	if err != nil {
		return 0, 0
	}
	return s.Blocked, s.ReclaimedTok
}

// Activities is what the coach did today and over the last week, in
// that order. A ledger that cannot be read counts no moves.
func Activities(l Ledger, now time.Time) []Activity {
	var entries []ft.Entry
	if l != nil {
		if es, err := l.Load(); err == nil {
			entries = es
		}
	}
	y, m, d := now.Date()
	periods := []Activity{
		{Period: PeriodToday, Since: time.Date(y, m, d, 0, 0, 0, 0, now.Location())},
		{Period: PeriodWeek, Since: now.Add(-ActivityDays * 24 * time.Hour)},
	}
	for i := range periods {
		a := &periods[i]
		for _, e := range entries {
			if e.Type != ft.EntryOffer || e.Channel != ft.ChannelMove || e.Power != config.PowerModels || e.At.Before(a.Since) {
				continue
			}
			if e.Kind == DefaultModelKind {
				a.DefaultModelMoves++
			} else {
				a.SubagentMoves++
			}
		}
		a.RefusedRereads, a.RefusedTokens = RefusedRereads(a.Since)
		a.Summary = a.words()
	}
	return periods
}

func (a Activity) words() string {
	var parts []string
	switch a.SubagentMoves {
	case 0:
	case 1:
		parts = append(parts, "moved 1 subagent to a cheaper model")
	default:
		parts = append(parts, fmt.Sprintf("moved %d subagents to a cheaper model", a.SubagentMoves))
	}
	switch a.DefaultModelMoves {
	case 0:
	case 1:
		parts = append(parts, "started new sessions on a cheaper model")
	default:
		parts = append(parts, fmt.Sprintf("started new sessions on a cheaper model %d times", a.DefaultModelMoves))
	}
	switch a.RefusedRereads {
	case 0:
	case 1:
		parts = append(parts, "refused 1 re-read")
	default:
		parts = append(parts, fmt.Sprintf("refused %d re-reads", a.RefusedRereads))
	}
	return strings.Join(parts, " · ")
}

// PresetSummary is a preset by name and what it does, for a surface that
// offers the choice.
type PresetSummary struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// PresetSummaries lists the presets, least autonomous first.
func PresetSummaries() []PresetSummary {
	ps := Presets()
	out := make([]PresetSummary, 0, len(ps))
	for _, p := range ps {
		out = append(out, PresetSummary{Name: p.Name, Summary: p.Summary})
	}
	return out
}
