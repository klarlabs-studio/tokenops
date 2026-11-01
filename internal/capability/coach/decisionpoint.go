package coach

import (
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	ft "go.klarlabs.de/tokenops/internal/contexts/coaching/followthrough"
)

// The decision points the coach speaks at (ft.PointToolCall, …).
const (
	PointToolCall    = ft.PointToolCall
	PointPrepareWork = ft.PointPrepareWork
	PointSubagent    = ft.PointSubagent
	PointModelChoice = ft.PointModelChoice
)

// Retirement is a tip kind the coach retired, and why.
type Retirement = ft.Retirement

// DecisionTip is a tip given to the agent at a decision point. It names
// one object and carries no file contents or prompt.
type DecisionTip struct {
	// ID names the tip in the follow-through ledger; empty until it is
	// delivered and recorded.
	ID string `json:"id,omitempty"`
	// Kind is the kind of tip, by which it is throttled, quieted and
	// retired ("window_fit", "model_fit", "subagent_model", …).
	Kind string `json:"kind"`
	// Point is the decision point it was given at.
	Point string `json:"point"`
	// Object is the one thing it names: a file, a search, a subagent
	// type, a model or a window.
	Object string `json:"object"`
	// Message is the tip as the agent reads it.
	Message string `json:"message"`
}

// TipGate decides whether the coach may give a decision-point tip. It
// reads the ledger once, on the first question, since most decisions
// have no tip due.
type TipGate struct {
	ledger   Ledger
	now      time.Time
	throttle ft.Throttle
	// inform requires the inform power on and verbosity not quiet, as
	// the coach's tips do; off leaves that to the caller (the per-turn
	// model advice follows the models power and its own quiet rule).
	inform  bool
	report  Report
	loaded  bool
	entries []ft.Entry
}

// NewTipGate is the gate for tips under cfg. The inform power off or
// verbosity quiet silences every tip it is asked about.
func NewTipGate(cfg config.Config, l Ledger, now time.Time) *TipGate {
	g := newGate(cfg, l, now)
	g.inform = true
	g.report = Build(cfg)
	return g
}

// NewAdviceGate is the gate for the per-turn model advice: only the
// throttle and the retired kinds hold it, since the models power and the
// advice's own verbosity rule decide the rest.
func NewAdviceGate(cfg config.Config, l Ledger, now time.Time) *TipGate {
	return newGate(cfg, l, now)
}

func newGate(cfg config.Config, l Ledger, now time.Time) *TipGate {
	perHour, cooldown := cfg.CoachTipThrottle()
	return &TipGate{ledger: l, now: now, throttle: ft.Throttle{PerHour: perHour, Cooldown: cooldown}}
}

func (g *TipGate) load() []ft.Entry {
	if !g.loaded {
		g.loaded = true
		if g.ledger != nil {
			if e, err := g.ledger.Load(); err == nil {
				g.entries = e
			}
		}
	}
	return g.entries
}

// Held says why a tip of power and kind naming object must not be given
// in session now, or "" when it may: the inform power is off or verbosity
// quiet (for tips), the kind is quieted or retired, the session has had
// its tips for the hour, or the same tip was given within the cool-down.
// A ledger that cannot be read holds nothing back but the dials.
func (g *TipGate) Held(session, power, kind, object string) string {
	if g == nil {
		return "no gate"
	}
	if g.inform {
		if g.report.Effective(config.PowerInform) == config.AutonomyOff {
			return "inform off"
		}
		if g.report.Verbosity == config.VerbosityQuiet {
			return "quiet"
		}
	}
	entries := g.load()
	if ft.Silenced(entries, g.now)(power, kind) {
		return "quieted"
	}
	return g.throttle.Admit(entries, session, kind, object, g.now)
}

// Deliver gives the tips the gate admits, records each one as delivered
// to the agent at its decision point, and returns them with their IDs.
// ref is a reference the tip can be settled by later, such as the
// workflow_id tokenops_prepare_work issued; it may be empty. A tip held
// back is dropped.
func (g *TipGate) Deliver(session, ref string, tips []DecisionTip) []DecisionTip {
	var out []DecisionTip
	for _, t := range tips {
		if g.Held(session, config.PowerInform, t.Kind, t.Object) != "" {
			continue
		}
		t.ID = NewID()
		e := tipAt(g.now, t.ID, session, t.Kind, t.Point, t.Object, ref)
		if g.ledger != nil {
			_ = g.ledger.Append(e)
		}
		// Later questions in the same call see it, so two tips naming
		// one object, or a full hour, hold each other back.
		g.entries = append(g.load(), e)
		out = append(out, t)
	}
	return out
}

// RecordTipAt records a tip given to the agent at a decision point,
// naming the one object it is about. ref is what can settle it later,
// such as a workflow_id; it may be empty.
func RecordTipAt(l Ledger, now time.Time, id, session, kind, point, object, ref string) {
	if l == nil || id == "" {
		return
	}
	_ = l.Append(tipAt(now, id, session, kind, point, object, ref))
}

func tipAt(now time.Time, id, session, kind, point, object, ref string) ft.Entry {
	return ft.Entry{
		Type: ft.EntryOffer, ID: id, At: now.UTC(), Session: session,
		Power: config.PowerInform, Channel: ft.ChannelTip, Kind: kind, Via: ft.ViaAgent,
		Point: point, Object: object, Ref: ref,
	}
}

// RecordAdviceAt is RecordAdvice for advice given at a decision point,
// naming the one object it is about (the model advised).
func RecordAdviceAt(l Ledger, now time.Time, id, session, power, kind, from, to, point, object string) {
	if l == nil || id == "" {
		return
	}
	_ = l.Append(ft.Entry{
		Type: ft.EntryOffer, ID: id, At: now.UTC(), Session: session,
		Power: power, Channel: ft.ChannelAdvice, Kind: kind, From: from, To: to,
		Point: point, Object: object,
	})
}

// RecordAgentOutcome settles a tip given to the agent at a decision point
// from what the agent did next: followed, or ignored with what it did
// instead. The agent acting again after the tip was in its context is
// the evidence it was seen, so the tip is recorded as seen by the agent;
// only then can ignoring it count toward retiring its kind.
func RecordAgentOutcome(l Ledger, now time.Time, id, kind string, followed bool, instead, evidence string) {
	if l == nil || id == "" {
		return
	}
	o := ft.OutcomeIgnored
	if followed {
		o, instead = ft.OutcomeFollowed, ""
	}
	_ = l.Append(
		ft.Entry{Type: ft.EntrySeen, ID: id, At: now.UTC(), Via: ft.ViaAgent},
		ft.Entry{Type: ft.EntryResolve, ID: id, At: now.UTC(), Kind: kind, Outcome: o, Instead: instead, Evidence: evidence},
	)
}

// ReinstateKind brings back tip or advice kinds the coach retired: kind,
// or every retired kind for "all". It returns the retirements it ended;
// the evidence before now no longer counts toward retiring them again.
func ReinstateKind(l Ledger, now time.Time, kind string) ([]Retirement, error) {
	if l == nil {
		return nil, nil
	}
	kind = strings.TrimSpace(kind)
	var ended []Retirement
	var entries []ft.Entry
	for _, s := range History(l, now) {
		if s.Retired == nil || (kind != "all" && s.Kind != kind) {
			continue
		}
		ended = append(ended, *s.Retired)
		entries = append(entries, ft.Entry{Type: ft.EntryReinstated, At: now.UTC(), Power: s.Power, Kind: s.Kind})
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return ended, l.Append(entries...)
}

// RetiredKinds lists the tip and advice kinds the coach has retired.
func RetiredKinds(sums []ft.Summary) []Retirement {
	var out []Retirement
	seen := map[string]bool{}
	for _, s := range sums {
		if s.Retired == nil || seen[s.Power+"/"+s.Kind] {
			continue
		}
		seen[s.Power+"/"+s.Kind] = true
		out = append(out, *s.Retired)
	}
	return out
}
