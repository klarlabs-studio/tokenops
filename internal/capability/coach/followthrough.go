package coach

import (
	"context"
	"strings"

	"crypto/rand"
	"encoding/hex"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/routers"
	"go.klarlabs.de/tokenops/internal/capability/savings"

	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

// Ledger is where the coach's follow-through is kept.
type Ledger interface {
	Append(entries ...ft.Entry) error
	Load() ([]ft.Entry, error)
}

// Resolution is the outcome of one earlier piece of advice.
type Resolution struct {
	ID       string
	Kind     string
	Followed bool
	Evidence string
}

// RecordAdvice records advice the coach gave, under the ID it was given.
func RecordAdvice(l Ledger, now time.Time, id, session, power, kind, from, to string) {
	if l == nil || id == "" {
		return
	}
	_ = l.Append(ft.Entry{
		Type: ft.EntryOffer, ID: id, At: now.UTC(), Session: session,
		Power: power, Channel: ft.ChannelAdvice, Kind: kind, From: from, To: to,
	})
}

// RecordApproval records a change the coach proposed and put to the
// operator for approval.
func RecordApproval(l Ledger, now time.Time, id, session, power, kind, from, to string) {
	RecordApprovalBecause(l, now, id, session, power, kind, from, to, "")
}

// RecordApprovalBecause is RecordApproval with the evidence the proposal
// rested on beyond the work itself, such as the plan running short.
func RecordApprovalBecause(l Ledger, now time.Time, id, session, power, kind, from, to, evidence string) {
	if l == nil || id == "" {
		return
	}
	_ = l.Append(ft.Entry{
		Type: ft.EntryOffer, ID: id, At: now.UTC(), Session: session,
		Power: power, Channel: ft.ChannelApproval, Kind: kind, From: from, To: to, Evidence: evidence,
	})
}

// Where a tip reached the operator, for RecordTip and RecordSeen.
const (
	ViaStopHook   = ft.ViaStopHook
	ViaStatusLine = ft.ViaStatusLine
	ViaAmbient    = ft.ViaAmbient
)

// RecordTip records a tip the coach gave about the session, under the ID
// it was given, and where it was delivered (ft.ViaStopHook). Tips belong
// to the inform power.
func RecordTip(l Ledger, now time.Time, id, session, kind, via string) {
	if l == nil || id == "" {
		return
	}
	_ = l.Append(ft.Entry{
		Type: ft.EntryOffer, ID: id, At: now.UTC(), Session: session,
		Power: config.PowerInform, Channel: ft.ChannelTip, Kind: kind, Via: via,
	})
}

// RecordSeen records that the offer id was shown where the operator sees
// it (ft.ViaStatusLine). Without it a tip that was let pass is unseen,
// not ignored, and cannot quiet the coach.
func RecordSeen(l Ledger, now time.Time, id, via string) {
	if l == nil || id == "" {
		return
	}
	_ = l.Append(ft.Entry{Type: ft.EntrySeen, ID: id, At: now.UTC(), Via: via})
}

// RecordResolutions records what became of earlier advice.
func RecordResolutions(l Ledger, now time.Time, rs []Resolution) {
	if l == nil || len(rs) == 0 {
		return
	}
	entries := make([]ft.Entry, 0, len(rs))
	for _, r := range rs {
		o := ft.OutcomeIgnored
		if r.Followed {
			o = ft.OutcomeFollowed
		}
		entries = append(entries, ft.Entry{Type: ft.EntryResolve, ID: r.ID, At: now.UTC(), Kind: r.Kind, Outcome: o, Evidence: r.Evidence})
	}
	_ = l.Append(entries...)
}

// RecordMove records a change the coach made on its own. Whether it stood
// is decided later, from whether the operator lowered the power.
func RecordMove(l Ledger, now time.Time, id, session, power, kind, from, to string) {
	RecordMoveBecause(l, now, id, session, power, kind, from, to, "")
}

// RecordMoveBecause is RecordMove with the evidence the move rested on
// beyond the work itself, such as the plan running short, so the move
// can be explained and its follow-through read against it.
func RecordMoveBecause(l Ledger, now time.Time, id, session, power, kind, from, to, evidence string) {
	if l == nil || id == "" {
		return
	}
	_ = l.Append(ft.Entry{
		Type: ft.EntryOffer, ID: id, At: now.UTC(), Session: session,
		Power: power, Channel: ft.ChannelMove, Kind: kind, From: from, To: to, Evidence: evidence,
	})
}

// RecordSubagentMove is RecordMoveBecause for a subagent the coach moved,
// carrying the Agent call's tool_use_id so the call's outcome can be read
// back from the transcript and the move judged (VerifyMoves).
func RecordSubagentMove(l Ledger, now time.Time, id, session, toolUseID, kind, from, to, evidence string) {
	if l == nil || id == "" {
		return
	}
	_ = l.Append(ft.Entry{
		Type: ft.EntryOffer, ID: id, At: now.UTC(), Session: session, Power: config.PowerModels,
		Channel: ft.ChannelMove, Kind: kind, From: from, To: to, Evidence: evidence, Ref: toolUseID,
	})
}

// RecordSubagentApproval is RecordApprovalBecause for a subagent move put
// to the operator, carrying the Agent call's tool_use_id: an approved
// move is judged like one the coach made itself.
func RecordSubagentApproval(l Ledger, now time.Time, id, session, toolUseID, kind, from, to, evidence string) {
	if l == nil || id == "" {
		return
	}
	_ = l.Append(ft.Entry{
		Type: ft.EntryOffer, ID: id, At: now.UTC(), Session: session, Power: config.PowerModels,
		Channel: ft.ChannelApproval, Kind: kind, From: from, To: to, Evidence: evidence, Ref: toolUseID,
	})
}

// Quieter returns whether advice of a power and kind has been ignored
// often enough that the coach stops offering it. A ledger that cannot be
// read quiets nothing: missing evidence must not silence the coach.
func Quieter(l Ledger, now time.Time) func(power, kind string) bool {
	none := func(string, string) bool { return false }
	if l == nil {
		return none
	}
	entries, err := l.Load()
	if err != nil || len(entries) == 0 {
		return none
	}
	records := ft.Fold(entries, now)
	return func(power, kind string) bool { return ft.Quieted(records, power, kind, now) }
}

// History is the follow-through evidence per kind of intervention.
func History(l Ledger, now time.Time) []ft.Summary {
	if l == nil {
		return nil
	}
	entries, err := l.Load()
	if err != nil {
		return nil
	}
	return ft.Summarize(ft.Fold(entries, now), now)
}

// CountMoves is how many changes the coach has made on its own for power.
func CountMoves(l Ledger, power string) int {
	if l == nil {
		return 0
	}
	entries, err := l.Load()
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.Type == ft.EntryOffer && e.Channel == ft.ChannelMove && e.Power == power {
			n++
		}
	}
	return n
}

// rungRank orders the autonomy rungs from least to most autonomous.
var rungRank = map[string]int{
	config.AutonomyOff: 0, config.AutonomyAdvise: 1,
	config.AutonomyAsk: 2, config.AutonomyAutonomous: 3,
}

// lowered lists the powers whose effective rung fell from before to after.
// Lowering a power right after the coach acted is how an operator undoes
// what it did, so each one is recorded against its recent moves.
func lowered(before, after Report, now time.Time) []ft.Entry {
	var out []ft.Entry
	for _, a := range after.Powers {
		b := before.Effective(a.Name)
		if rungRank[a.Effective] < rungRank[b] {
			out = append(out, ft.Entry{Type: ft.EntryLowered, At: now.UTC(), Power: a.Name, From: b, To: a.Effective})
		}
	}
	return out
}

// Apply changes the coach's settings in the config at path: it applies
// change, validates, writes, records any power it lowered, and returns
// the coach as it now stands. The CLI and the MCP tool both set the coach
// through it.
func Apply(path string, l Ledger, levers ContextLevers, now time.Time, change func(*config.Config)) (Report, error) {
	cfg, err := config.ReadMutable(path)
	if err != nil {
		return Report{}, err
	}
	before := Build(cfg)
	change(&cfg)
	if err := cfg.Coach.Validate(); err != nil {
		return Report{}, err
	}
	if err := config.WriteMutable(path, cfg); err != nil {
		return Report{}, err
	}
	after := Build(cfg)
	if l != nil {
		_ = l.Append(lowered(before, after, now)...)
	}
	after.Compaction = reconcileContext(before, after, levers, l, now)
	return after, nil
}

// NewID names a new intervention in the ledger.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Status is the coach as Build reports it, with its follow-through
// evidence.
func Status(cfg config.Config, l Ledger, levers ContextLevers, now time.Time) Report {
	r := Build(cfg)
	r.Preset = CurrentPreset(cfg)
	r.FollowThrough = History(l, now)
	r.Activity = Activities(l, now)
	r.Presets = PresetSummaries()
	if levers != nil {
		r.Compaction = levers.Check()
	}
	r.Routers = DetectRouters()
	if v := Verdicts(MoveStore(), now); len(v.Lines) > 0 {
		r.Moves = &v
	}
	r.Graduation = savings.Graduations(savings.Build(context.Background(), SavingsDeps(), now), r.Effective, r.FollowThrough)
	for i, p := range r.Powers {
		if p.Name != config.PowerModels || len(r.Routers) == 0 {
			continue
		}
		where := make([]string, 0, len(r.Routers))
		for _, rt := range r.Routers {
			where = append(where, rt.StandDown())
		}
		r.Powers[i].Note = strings.TrimSpace(p.Note + " Stands down where an external router decides: " + strings.Join(where, "; ") + ".")
	}
	return r
}

// SavingsDeps reads the savings ledger for the graduation; a variable so
// tests do not read the machine's ledgers. It leaves git out: Status
// serves the glance, which is asked often, and `tokenops verify` is where
// commits are read.
var SavingsDeps = func() savings.Deps {
	d := savings.DefaultDeps()
	d.Commits = nil
	return d
}

// DetectRouters finds the external routers in each harness's path; a
// variable so tests do not read the machine's settings.
var DetectRouters = func() []routers.InPath { return routers.Detect(routers.Sources{}) }
