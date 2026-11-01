package fmtinsight

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/outcomes"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn"
	"go.klarlabs.de/tokenops/internal/infra/fmtindex"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// ExperimentReport compares the fmt experiment's arms on outcomes.
type ExperimentReport = fmtlearn.ExperimentReport

// The experiment's arms.
const (
	ArmFmt = fmtlearn.ArmFmt
	ArmRaw = fmtlearn.ArmRaw
)

// ExperimentArm draws a session's arm; see fmtlearn.ExperimentArm.
func ExperimentArm(session string, rawPercent int) string {
	return fmtlearn.ExperimentArm(session, rawPercent)
}

// ExperimentLines renders the comparison for a terminal.
func ExperimentLines(r ExperimentReport) []string { return fmtlearn.ExperimentLines(r) }

// ParseExperiment reads an experiment setting as an operator writes it:
// off, on (half the sessions raw), or a percent from 0 to 50.
func ParseExperiment(s string) (int, error) {
	v := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "%")
	switch v {
	case "off":
		return 0, nil
	case "on":
		return config.DefaultFmtExperimentPercent, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > config.MaxFmtExperimentPercent {
		return 0, fmt.Errorf("experiment must be off, on, or a percent from 0 to %d, got %q", config.MaxFmtExperimentPercent, s)
	}
	return n, nil
}

// DefaultExperimentDays is how far back the report reads by default.
const DefaultExperimentDays = 30

// ExperimentOptions selects what the experiment report reads.
type ExperimentOptions struct {
	// RecoverDir is the recovery index the runs are recorded in; empty
	// is the default ~/.tokenops/recovery.
	RecoverDir string
	// Since drops runs and transcript entries older than this.
	Since time.Time
	// Transcripts reads the sessions' transcript records; nil reads
	// Claude Code's, the client whose session id fmt sees.
	Transcripts func(since time.Time) ([]agentdx.Record, error)
	// Outcomes are recorded results by execution id; an execution whose
	// id is a session id in the experiment counts for that session.
	Outcomes map[string]string
}

// Experiment reads the runs the experiment assigned and the sessions they
// ran in, and compares the arms. A transcript read that fails leaves the
// sessions without outcomes, which the report shows as too few, rather
// than failing.
func Experiment(rawPercent int, o ExperimentOptions) (ExperimentReport, error) {
	runs, err := fmtindex.Read(o.RecoverDir)
	if err != nil {
		return ExperimentReport{}, err
	}
	read := o.Transcripts
	if read == nil {
		read = func(since time.Time) ([]agentdx.Record, error) {
			return agentdx.ExtractAll(agentdx.ExtractOptions{Source: agentdx.SourceClaudeCode, Since: since})
		}
	}
	var records []agentdx.Record
	if hasExperimentRuns(runs, o.Since) {
		records, _ = read(o.Since)
	}
	sessions, mixed := SessionOutcomes(runs, records, o.Outcomes, o.Since)
	r := fmtlearn.CompareArms(sessions, rawPercent)
	r.Mixed = mixed
	return r, nil
}

// ExperimentOutcomes resolves recorded outcome events to one result per
// execution id, the strongest assessment winning as it does everywhere
// outcomes are read. Unknown results are dropped.
func ExperimentOutcomes(events []*eventschema.Envelope) map[string]string {
	byExec := map[string][]*eventschema.Envelope{}
	for _, env := range events {
		if env == nil || env.Association.Execution == "" {
			continue
		}
		if _, ok := env.Payload.(*eventschema.OutcomeEvent); !ok {
			continue
		}
		byExec[env.Association.Execution] = append(byExec[env.Association.Execution], env)
	}
	out := make(map[string]string, len(byExec))
	for exec, envs := range byExec {
		if r := outcomes.Resolve(envs).Result; r != "" && r != eventschema.OutcomeUnknown {
			out[exec] = string(r)
		}
	}
	return out
}

func hasExperimentRuns(runs []fmtlearn.Record, since time.Time) bool {
	for _, r := range runs {
		if r.Arm != "" && r.Session != "" && !r.TS.Before(since) {
			return true
		}
	}
	return false
}

// SessionOutcomes joins the runs the experiment assigned to the sessions
// they ran in. A session is in the arm its runs name; one whose runs name
// both is counted in mixed and left out. A subagent's records
// (<session>/<agent>) count for the session that started it: its commands
// ran in that session's arm.
func SessionOutcomes(runs []fmtlearn.Record, records []agentdx.Record, outcomes map[string]string, since time.Time) ([]fmtlearn.SessionOutcome, int) {
	byID := map[string]*fmtlearn.SessionOutcome{}
	var order []string
	mixedIDs := map[string]bool{}
	for _, r := range runs {
		if r.Type != fmtlearn.RecordCompress || r.Session == "" || r.Arm == "" || r.TS.Before(since) {
			continue
		}
		s, ok := byID[r.Session]
		if !ok {
			s = &fmtlearn.SessionOutcome{Session: r.Session, Arm: r.Arm}
			byID[r.Session] = s
			order = append(order, r.Session)
		}
		if s.Arm != r.Arm {
			mixedIDs[r.Session] = true
		}
		s.Runs++
		s.TokensSaved += r.TokensSaved
	}

	parent := func(id string) string {
		p, _, _ := strings.Cut(id, "/")
		return p
	}
	perSession := map[string][]agentdx.Record{}
	seen := map[string]map[string]bool{}
	for _, rec := range records {
		id := parent(rec.SessionID)
		s, ok := byID[id]
		if !ok {
			continue
		}
		perSession[id] = append(perSession[id], rec)
		if rec.Kind == agentdx.KindToolUse && rec.CallSignature != "" {
			if seen[rec.SessionID] == nil {
				seen[rec.SessionID] = map[string]bool{}
			}
			s.ToolCalls++
			if seen[rec.SessionID][rec.CallSignature] {
				s.RepeatCalls++
			}
			seen[rec.SessionID][rec.CallSignature] = true
		}
	}
	for id, recs := range perSession {
		s := byID[id]
		var tokens []int64
		for _, u := range agentdx.Units(recs) {
			if u.Turns == 0 {
				continue
			}
			s.Instructions++
			if u.FirstTry() {
				s.FirstTry++
			}
			if u.Reworked {
				s.Reworked++
			}
			tokens = append(tokens, u.Tokens)
		}
		s.TokensPerInstruction = median(tokens)
	}

	out := make([]fmtlearn.SessionOutcome, 0, len(order))
	for _, id := range order {
		if mixedIDs[id] {
			continue
		}
		s := *byID[id]
		s.Outcome = outcomes[id]
		out = append(out, s)
	}
	return out, len(mixedIDs)
}

func median(vs []int64) int64 {
	if len(vs) == 0 {
		return 0
	}
	sorted := append([]int64(nil), vs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
