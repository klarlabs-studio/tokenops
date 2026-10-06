// Package findings ranks what the coach and the session readers have
// observed into a short list a person can act on: a quota window that
// runs out before it resets, files the agent keeps re-reading, sessions
// past their budget, the single change that would most improve how
// sessions go, sources that cannot be read, and what the coach would say
// while it only observes. The CLI's glance, the menu bar and the daemon
// API all show this list (ADR 0010).
//
// Findings are derived: counts, shares, durations and money. No prompt,
// file content or transcript text reaches them.
package findings

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/capability/sessions"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/infra/coachhook"
	"go.klarlabs.de/tokenops/internal/infra/readguard"
)

// Levels, most urgent first.
const (
	LevelWarn   = "warn"
	LevelNotice = "notice"
	LevelInfo   = "info"
)

// Kinds name where a finding comes from.
const (
	KindQuota    = "quota"
	KindData     = "data"
	KindSessions = "sessions"
	KindWaste    = "waste"
	KindBudget   = "budget"
	KindCoach    = "coach"
)

// Finding is one observation: what was seen, the figures behind it, and
// what to do about it, when there is something to do.
type Finding struct {
	Kind     string `json:"kind"`
	Level    string `json:"level"`
	Title    string `json:"title"`
	Evidence string `json:"evidence,omitempty"`
	Action   string `json:"action,omitempty"`
}

// Report is the ranked list.
type Report struct {
	Findings []Finding `json:"findings"`
	// SessionsReadAt is when the session analysis last ran; it runs in
	// the daemon's background, so it can lag the rest by hours. Absent
	// when it has not run yet.
	SessionsReadAt *time.Time `json:"sessions_read_at,omitempty"`
}

// Inputs are the readings findings are drawn from; any may be missing.
type Inputs struct {
	Glance    *headroom.Glance
	Coach     *coach.Report
	CoachHook *coachhook.Stats
	ReadGuard *readguard.Stats
	Sessions  *SessionsSnapshot
}

// Compute ranks every finding in, most urgent first.
func Compute(in Inputs) Report {
	var out []Finding
	if in.Glance != nil {
		out = append(out, quota(in.Glance.Headroom.Reports)...)
	}
	if in.Sessions != nil {
		out = append(out, sessionFindings(in.Sessions.DX.Warnings, in.Sessions.DX.Recommendation)...)
	}
	out = append(out, waste(in.ReadGuard, in.Coach)...)
	out = append(out, budget(in.CoachHook)...)
	out = append(out, coaching(in.Coach, in.CoachHook)...)
	rank := map[string]int{LevelWarn: 0, LevelNotice: 1, LevelInfo: 2}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Level] < rank[out[j].Level] })
	r := Report{Findings: out}
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	if in.Sessions != nil && !in.Sessions.ComputedAt.IsZero() {
		at := in.Sessions.ComputedAt
		r.SessionsReadAt = &at
	}
	return r
}

// vendorNames names each provider as its users do.
var vendorNames = map[string]string{"anthropic": "Claude", "openai": "Codex", "gemini": "Gemini", "github": "Copilot", "cursor": "Cursor"}

func vendor(r plans.HeadroomReport) string {
	if v, ok := vendorNames[r.Provider]; ok {
		return v
	}
	return r.Display
}

// windowWords names a window for a sentence: "weekly window".
func windowWords(name string) string {
	if model, ok := strings.CutPrefix(name, "week ("); ok {
		return strings.TrimSuffix(model, ")") + " weekly window"
	}
	switch name {
	case "5h":
		return "session window"
	case "week":
		return "weekly window"
	case "day":
		return "daily window"
	}
	return name + " window"
}

// quota warns about every window that runs out before it resets, and
// names a plan with room to take the work meanwhile.
func quota(reports []plans.HeadroomReport) []Finding {
	var out []Finding
	for _, r := range reports {
		for _, w := range r.Windows {
			p := w.Pace
			if p == nil || p.Status != plans.PaceAhead || p.LastsToReset {
				continue
			}
			f := Finding{
				Kind:  KindQuota,
				Level: LevelWarn,
				Title: fmt.Sprintf("%s's %s runs out in %s at this pace", vendor(r), windowWords(w.Name), human(p.RunsOutIn)),
				Evidence: fmt.Sprintf("%.0f%% left, %.0f points ahead of an even pace; it resets in %s",
					math.Max(0, 100-w.UsedPct), p.DeltaPct, human(resetsIn(w))),
			}
			if alt, pct, ok := roomiest(reports, r.Provider); ok {
				f.Action = fmt.Sprintf("Put the work that can move on %s (%.0f%% left) until it resets.", alt, math.Max(0, 100-pct))
			} else {
				f.Action = "Pace the remaining work, or keep the long tasks for after the reset."
			}
			out = append(out, f)
		}
	}
	return out
}

func resetsIn(w plans.VendorWindow) time.Duration {
	d, _ := time.ParseDuration(w.ResetsIn)
	return d
}

// roomiest is the other plan with the most room, when it has plenty.
func roomiest(reports []plans.HeadroomReport, except string) (string, float64, bool) {
	best, bestPct := "", math.Inf(1)
	for _, r := range reports {
		if r.Provider == except || len(r.Windows) == 0 {
			continue
		}
		busiest := 0.0
		for _, w := range r.Windows {
			busiest = math.Max(busiest, w.UsedPct)
		}
		if busiest < bestPct {
			best, bestPct = vendor(r), busiest
		}
	}
	return best, bestPct, best != "" && bestPct < 60
}

// sessionFindings turns the session analysis into findings: a source it
// could not read, and the single change it recommends.
func sessionFindings(warnings []string, rec *sessions.Recommendation) []Finding {
	var out []Finding
	for _, w := range warnings {
		out = append(out, unreadable(w))
	}
	if rec != nil {
		out = append(out, Finding{Kind: KindSessions, Level: LevelNotice, Title: rec.Title, Evidence: rec.Evidence, Action: sentence(rec.Action)})
	}
	return out
}

// unreadable words a reader's warning; the reader's own message is the
// evidence, since it names what failed.
func unreadable(warning string) Finding {
	f := Finding{Kind: KindData, Level: LevelWarn, Title: "Some sessions cannot be read", Evidence: warning}
	if strings.Contains(warning, "opencode") {
		f.Title = "opencode sessions cannot be read"
		if strings.Contains(warning, "schema") {
			f.Evidence = "opencode's database is in a newer format than this version reads, so its sessions are missing from every figure"
		}
	}
	return f
}

// waste reports re-reads of unchanged files: refused ones as a result,
// observed ones as a change to make.
func waste(s *readguard.Stats, c *coach.Report) []Finding {
	if s == nil {
		return nil
	}
	var out []Finding
	if s.WouldBlock > 0 && (c == nil || c.Effective(config.PowerWaste) != config.AutonomyAutonomous) {
		out = append(out, Finding{
			Kind:     KindWaste,
			Level:    LevelNotice,
			Title:    fmt.Sprintf("Agents re-read %d unchanged files in full", s.WouldBlock),
			Evidence: fmt.Sprintf("about %s tokens of context spent on files already read, across %d sessions", tokens(s.ReclaimableTok), s.DistinctSessions),
			Action:   "`tokenops coach set waste autonomous` lets the read guard refuse them.",
		})
	}
	if s.Blocked > 0 {
		out = append(out, Finding{
			Kind:     KindWaste,
			Level:    LevelInfo,
			Title:    fmt.Sprintf("The read guard refused %d re-reads", s.Blocked),
			Evidence: fmt.Sprintf("about %s tokens kept out of context", tokens(s.ReclaimedTok)),
		})
	}
	return out
}

// budget reports sessions that ran past their budget.
func budget(s *coachhook.Stats) []Finding {
	if s == nil {
		return nil
	}
	over := s.AlertsByTier["100%"]
	if over == 0 {
		return nil
	}
	return []Finding{{
		Kind:     KindBudget,
		Level:    LevelNotice,
		Title:    fmt.Sprintf("%d of %d sessions ran past their budget", over, s.DistinctSessions),
		Evidence: fmt.Sprintf("the largest came to about %s at API prices", money(s.MaxCumulativeUSD)),
		Action:   "`tokenops coach stats` shows how far past each one went.",
	}}
}

// coaching reports a coach that only observes, and kinds of advice it
// stopped giving because they kept being ignored.
func coaching(c *coach.Report, s *coachhook.Stats) []Finding {
	if c == nil {
		return nil
	}
	var out []Finding
	if c.Off() {
		f := Finding{Kind: KindCoach, Level: LevelInfo, Title: "The coach is observing and says nothing",
			Action: "`tokenops coach preset advise` lets it speak, once per kind of thing."}
		if s != nil {
			if tips := s.Alerts + total(s.QuotaNudges) + s.PromotionNudges + s.CompactTips; tips > 0 {
				f.Evidence = fmt.Sprintf("it would have given %d tips so far", tips)
			}
		}
		out = append(out, f)
	}
	for _, sum := range c.FollowThrough {
		if !sum.Quiet {
			continue
		}
		out = append(out, Finding{
			Kind:     KindCoach,
			Level:    LevelInfo,
			Title:    fmt.Sprintf("The coach went quiet on %s", kindWords(sum.Kind)),
			Evidence: fmt.Sprintf("%d of %d followed", sum.Followed, sum.Resolved()),
		})
	}
	return out
}

func kindWords(kind string) string {
	switch {
	case kind == "compact_now":
		return "compact tips"
	case strings.HasPrefix(kind, "quota_"):
		return "quota tips"
	}
	return strings.ReplaceAll(kind, "_", " ") + " advice"
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// sentence capitalises s and ends it with a full stop.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

func human(d time.Duration) string {
	d = d.Round(time.Minute)
	days, hours, mins := int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour), int(d%time.Hour/time.Minute)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

func tokens(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

func money(usd float64) string {
	if usd < 100 {
		return fmt.Sprintf("$%.2f", usd)
	}
	digits := fmt.Sprintf("%.0f", usd)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return "$" + b.String()
}
