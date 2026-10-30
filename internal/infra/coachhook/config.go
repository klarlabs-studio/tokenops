package coachhook

import (
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
)

// Config tunes the coach. Enabled=false makes Evaluate observe-only (it still
// accumulates spend and records the ledger, but never nudges and never latches
// a tier), so an operator can watch a session's cost without being nudged.
type Config struct {
	// BudgetUSD is the per-session API-equivalent budget the fractions are
	// measured against.
	BudgetUSD float64
	// Tiers are the budget fractions (e.g. 0.50, 0.75, 1.00) at which to
	// nudge. Each fires at most once per session (latched).
	Tiers []float64
	// Quota is the live window reading for a flat-rate plan the session
	// runs on. When set it replaces the dollar ladder: the coach warns
	// when both burn-rate look-backs run a window out before its reset,
	// and says when one is used up, each once per window across sessions.
	Quota *Quota
	// FlatPlan says the session's provider is on a flat-rate plan. Its
	// dollar figure is then a counterfactual nobody pays, so the coach
	// never speaks the dollar ladder there, with or without a Quota.
	FlatPlan bool
	// ReadingLost, on a flat-rate plan with no live Quota, says why the
	// plan's window cannot be read and what brings it back. Empty when
	// nothing is known (the meter was never set up): the coach is silent.
	ReadingLost string
	// Verbosity is how much the coach says (ADR 0006): quiet speaks only
	// when work is about to stop, verbose explains; empty is normal.
	Verbosity string
	// OverBudgetStep re-alerts every additional step once the budget is
	// exceeded: with 1.00 the coach also fires at 200%, 300%, … of budget.
	// Zero disables over-budget escalation.
	OverBudgetStep float64
	// Enabled gates nudging. When false the coach observes (accumulate +
	// ledger only) and never latches a tier.
	Enabled bool
	// Quiet bounds how often the coach may speak unprompted in one
	// session. Zero values mean the per-finding latches are the whole
	// policy, which is what the hook did before the key existed.
	Quiet Quiet
	// Rates prices a turn against the card in effect at its timestamp.
	//
	// Nil falls back to the embedded baseline, which is what this package
	// used unconditionally until it turned out the baseline is not the
	// card the rest of tokenops prices with: the daemon layers the
	// snapshots under ~/.tokenops/pricing on top of it. A machine whose
	// snapshot knew gpt-5.5 still had its coach-hook budget measured
	// against a card that did not, so the tiers could not fire.
	Rates func(at time.Time) spend.Table
	// TurnLedgerDir is where a Cursor turn's tokens are recorded for the
	// daemon to ingest. Empty takes the default.
	TurnLedgerDir string
	// Promotion is the case for letting the read guard start refusing
	// redundant re-reads, built from the operator's own ledger. Empty
	// when the evidence does not justify it, when the guard is already
	// blocking, or when delivery is not `advise` — the level at which the
	// coach makes a case and waits for a human.
	//
	// The caller builds it rather than this package reading the guard's
	// ledger itself, because who is allowed to speak is a delivery
	// question, and delivery lives in the config rather than in the hook.
	Promotion string
	// NewID names a tip so its outcome can be recorded against it (ADR
	// 0006, follow-through). Nil records nothing.
	NewID func() string
	// CompactAtTokens is the context size past which a long stretch without
	// compacting earns a tip (compact_now): the live form of the
	// compact_earlier finding. Zero disables it.
	CompactAtTokens int64
	// Quieted reports that tips of a kind have been ignored often enough
	// that the coach stops giving them. Only early tiers can be quieted,
	// and verbose still gives them. Nil quiets nothing.
	Quieted func(kind string) bool
	// Held reports why a tip of kind must not be said in session because
	// the operator answered an earlier one: "snoozed" defers it,
	// "answered" closes the episode. Nil holds nothing.
	Held func(session, kind string) string
	// BudgetAnswers counts the budget tips the operator answered lately
	// and how many they called expected: the outcome that tunes the
	// derived session budget. Nil counts none.
	BudgetAnswers func() (expected, answered int)
	// HoldoutPercent is the share of sessions whose cost and drift tips
	// are held back and logged as shadow (#656): the randomized cohort a
	// realized saving is measured against. Zero opts out; it is clamped
	// to learning.MaxHoldoutPercent, and tips about work about to stop
	// are never held.
	HoldoutPercent int
	// Budgets are the operator's spend budget windows as the daemon last
	// measured them. Each window's warning, critical and spent tiers are
	// said once per window as tips of the "spend" family. Nil says
	// nothing about budgets.
	Budgets []boundaryledger.BudgetReading
	// SessionBiller names the provider that bills the session's turns,
	// given its latest model. A budget on another provider's bill is not
	// said in this session. Nil, or "", it cannot tell, and every budget
	// is said.
	SessionBiller func(model string) string
	// Alternatives reads the state of the operator's other plans. It is
	// called only when a used-up window is about to be stood down for, so
	// a hook that never gets there reads nothing. Nil knows of none.
	Alternatives func() []Alternative
	// MostlyUnseen reports that tips of a kind went mostly unseen over
	// the last week. Nil says none did.
	MostlyUnseen func(kind string) bool
}

// Quiet is the rate limit on the coach's proactive channel.
//
// Each finding already latches, so this exists for the case a latch
// cannot see: two *different* findings landing back to back. The floor
// defers — the suppressed finding stays unlatched and speaks once the
// floor has passed — and the cap drops, because a cap that queues is not
// a cap.
type Quiet struct {
	// MinInterval is the floor between two nudges in one session. Zero
	// means no floor.
	MinInterval time.Duration
	// MaxPerSession caps the nudges one session may carry. Zero means no
	// cap.
	MaxPerSession int
	// MaxPerDay is the daily interruption budget across sessions. Past
	// it, tips wait on the status line instead of interrupting, except
	// tips about work about to stop. Zero means no budget.
	MaxPerDay int
}

// silence reports why a nudge must not be spoken now, and whether the
// finding behind it should keep its latch so it can retry later.
//
// Returning the reason rather than a bare bool is what makes the policy
// auditable: it goes into the ledger, so `coach stats` can show that
// the coach had something to say and held it, rather than the operator
// having to infer a working rate limit from an absence.
func (q Quiet) silence(nudges int, last, now time.Time) (reason string, retry bool) {
	if q.MaxPerSession > 0 && nudges >= q.MaxPerSession {
		return "max_per_session", false
	}
	if q.MinInterval > 0 && !last.IsZero() && now.Sub(last) < q.MinInterval {
		return "min_interval", true
	}
	return "", false
}

// DefaultConfig returns the shipping defaults: enabled, $50 budget, 50/75/100%
// tiers, and over-budget escalation every additional full budget.
func DefaultConfig() Config {
	return Config{
		BudgetUSD:      DefaultBudgetUSD,
		Tiers:          DefaultTiers(),
		OverBudgetStep: 1.00,
		Enabled:        true,
	}
}

// Decision is the coach's verdict for one Stop event.
type Decision struct {
	// Nudge is true when the operator should be shown Message.
	Nudge bool
	// Message names the lever (compact / fresh session) and the numbers.
	Message string
	// CumulativeUSD is the session's total API-equivalent spend so far.
	CumulativeUSD float64
	// BudgetUSD is the budget the fraction is measured against.
	BudgetUSD float64
	// FiredFraction is the budget fraction whose boundary this Stop fired
	// (e.g. 0.50, 1.00, 2.00). Zero when no nudge fired.
	FiredFraction float64
	// ContextTokens is how full the model's context window was on the
	// newest turn. Zero when no turn was observed.
	ContextTokens int64
	// ContextWindow is the model's window size, when one is known for it.
	ContextWindow int64
	// StoodDown names the kind of tip the coach did not give because the
	// vendor's own surface says it (Suppressed is then
	// SuppressedVendorSpeaks), and VendorSurface the surface.
	StoodDown     string
	VendorSurface string
	// repeatBurn marks a run-out warning that already interrupted today
	// and mostly goes unseen: it waits on the status line.
	repeatBurn bool
	// Suppressed names the quiet rule that held a nudge back
	// ("min_interval", "max_per_session"), empty when nothing was held.
	// The coach had something to say; the operator asked it not to say it
	// yet.
	Suppressed string
	// Promotion is true when the nudge is the read-guard case rather than
	// a budget tier.
	Promotion bool
	// QuotaTier is 1.0 when this Stop said a plan window is used up.
	QuotaTier float64
	// QuotaBurn is true when this Stop warned that a plan window runs out
	// before its reset at the pace of both look-backs.
	QuotaBurn bool
	// quotaWindow is the window a quota tip was about, and runsOutAt when
	// a run-out warning said it runs out.
	quotaWindow plans.QuotaWindow
	runsOutAt   time.Time
	// SpendKind names the budget window tip this Stop gave (spend_75,
	// spend_over), and spend the window it was about.
	SpendKind string
	spend     boundaryledger.BudgetReading
	// ReadingLost is true when this Stop said the plan's window cannot be
	// read.
	ReadingLost bool
	// UnpricedModel names a model this session ran on that the rate card
	// does not know, empty when everything was priceable.
	//
	// Without it the session reports $0 and reads as a cheap one. That is
	// the failure this tool exists to find, wearing the tool's own
	// clothes: `gpt-5.5` and `gpt-5.6-luna` are what Codex runs today and
	// neither is in the shipped catalog, so every Codex session would
	// have looked free.
	UnpricedModel string
	// OfferID and TipKind name the tip this Stop gave, for the
	// follow-through ledger. Empty when none was given.
	OfferID string
	TipKind string
	// Resolved are earlier tips whose outcome this Stop settled.
	Resolved []TipResolution
	// CompactTip is true when the nudge is the compact_now tip.
	CompactTip bool
	// Ambient is true when a tip was due but goes to the status line as
	// the open tip instead of interrupting (Suppressed says why).
	Ambient bool
	// Holdout is true when a due tip was held back for the session's
	// holdout cohort. ShadowID and ShadowKind name it in the savings
	// ledger; ShadowResolved are earlier held tips this Stop settled.
	Holdout        bool
	ShadowID       string
	ShadowKind     string
	ShadowResolved []TipResolution
	// ExpectedUSD and ExpectedTokens are what acting on the tip given
	// (or held) this Stop is expected to save per later turn, and
	// Evidence the figures it rests on. Zero when no tip was due.
	ExpectedUSD    float64
	ExpectedTokens int64
	Evidence       string
}

// ratesAt resolves the card for a turn, falling back to the embedded
// baseline when no dated source was wired.
func (c Config) ratesAt(at time.Time) spend.Table {
	if c.Rates == nil {
		return spend.DefaultTable()
	}
	return c.Rates(at)
}
