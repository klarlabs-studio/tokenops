package teamwire

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
)

// The FinOps sections of a schema 2 upload (#685): what a team lead asks
// of a plan — are the seats used, what is avoidable, do the guardrails
// pay, which tier fits — as derived figures. Like a bucket, every string
// is an enumeration or a validated label and every figure a sum or a
// bounded share; no section holds a session, a model, a prompt or an
// allocation tag.

// Limits on the FinOps sections.
const (
	// MaxPlanDays bounds the plan-day rows: a few plans a day.
	MaxPlanDays = MaxDays * 8
	// MaxWaste bounds the waste rows.
	MaxWaste = MaxBuckets
	// MaxPlanAdvice bounds the advice rows: one per provider.
	MaxPlanAdvice = 32
	// MaxAdviceDays is the longest period a plan advice replays.
	MaxAdviceDays = 90
	// maxUSD bounds any one dollar figure: far above any real day, low
	// enough that a corrupt value is caught.
	maxUSD = 1e7
	// minutesPerDay bounds a day's blocked minutes.
	minutesPerDay = 24 * 60
)

// Waste classes: the waste taxonomy of #655, in attribution order. They
// mirror internal/contexts/coaching/waste.Classes(), which a test in
// internal/capability/teamshare holds this list to.
const (
	WasteReread       = "redundant_reread"
	WasteCacheRebuild = "cache_rebuild"
	WasteOverTiered   = "over_tiered_model"
	WasteRetryLoop    = "retry_loop"
	WasteOversized    = "oversized_context"
	WasteAbandoned    = "abandoned_session"
)

// WasteClasses lists every waste class.
var WasteClasses = []string{WasteReread, WasteCacheRebuild, WasteOverTiered, WasteRetryLoop, WasteOversized, WasteAbandoned}

// Plan advice vocabulary, mirroring internal/contexts/spend/plans (held
// to it by the same test).
var (
	// AdviceVerdicts are what a replay recommends.
	AdviceVerdicts = []string{"stay", "switch", "upgrade"}
	// AdviceKinds are the ways a period can be paid for.
	AdviceKinds = []string{"plan", "plan_overflow", "api"}
	// AdviceConfidences are how far a replay's answer can be trusted.
	AdviceConfidences = []string{"low", "medium", "high"}
	// ProviderlessPlans are bindings that belong to no one provider: a
	// spend limit, or the windows a vendor reports for an unnamed tier.
	ProviderlessPlans = []string{"pay-as-you-go", "subscription"}
)

// ValidProvider reports whether s is a provider in the registry.
func ValidProvider(s string) bool {
	_, ok := providers.Lookup(s)
	return ok
}

// ValidPlan reports whether plan is a catalog plan of provider, by its
// current name or one the vendor retired, or a binding any provider may
// have.
func ValidPlan(provider, plan string) bool {
	if !ValidProvider(provider) {
		return false
	}
	if slices.Contains(ProviderlessPlans, plan) {
		return true
	}
	for _, p := range providers.Plans() {
		if p.Name == plan || slices.Contains(p.FormerNames, plan) {
			return p.Provider == provider
		}
	}
	return false
}

// CurrentPlan is the catalog name plan has now: a name a vendor retired
// (an older client still sends claude-max-20x for Claude Ultra) is the
// plan's current one, and any other name is returned as it is.
func CurrentPlan(plan string) string {
	for _, p := range providers.Plans() {
		if slices.Contains(p.FormerNames, plan) {
			return p.Name
		}
	}
	return plan
}

// ValidWasteClass reports whether s is a waste class.
func ValidWasteClass(s string) bool { return slices.Contains(WasteClasses, s) }

// PlanDay is one plan's use on one UTC day, after the cloud commitment
// KPIs (#657): utilization is how full the plan's longest window kind got,
// coverage how much of the provider's usage the plan carried rather than
// a per-token bill. A figure that could not be measured is absent.
type PlanDay struct {
	Day      string `json:"day"`
	Provider string `json:"provider"`
	// Plan is the catalog plan bound for the provider that day.
	Plan string `json:"plan"`
	// UtilizationPct is the mean peak, 0 to 100, of the longest window
	// kind's windows that reset that day; UtilizationWindows how many
	// there were, to weight days and machines by.
	UtilizationPct     *float64 `json:"utilization_pct,omitempty"`
	UtilizationWindows int64    `json:"utilization_windows"`
	// CoveragePct is PlanTokens as a share, 0 to 100, of PlanTokens and
	// BilledTokens; absent when both are zero.
	CoveragePct  *float64 `json:"coverage_pct,omitempty"`
	PlanTokens   int64    `json:"plan_tokens"`
	BilledTokens int64    `json:"billed_tokens"`
	// Lockouts counts the plan's windows that ran out that day;
	// BlockedMinutes how long, from running out to the reset, within the
	// day, overlapping windows counted once.
	Lockouts       int64 `json:"lockouts"`
	BlockedMinutes int64 `json:"blocked_minutes"`
}

// Waste is the avoidable usage one machine's work of one kind, in one
// repository, on one UTC day, had in one waste class (#655). Each token is
// in at most one class, so rows add up.
type Waste struct {
	Day   string `json:"day"`
	Repo  string `json:"repo"`
	Kind  string `json:"kind"`
	Class string `json:"class"`
	// Turns counts the turns with avoidable tokens of the class.
	Turns  int64 `json:"turns"`
	Tokens int64 `json:"tokens"`
	// APIEquivalentUSD is the avoidable tokens' value at list price.
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
}

// SavingsDay is what the guardrails saved on one UTC day, beside what
// stayed avoidable (#656): RealizedUSD of RealizedUSD plus AvoidableUSD is
// the share of the avoidable spend that was saved. Both are sums, so the
// share is taken after adding across machines.
type SavingsDay struct {
	Day string `json:"day"`
	// Interventions counts the tips shown and refusals made; Held the
	// tips held back for the randomized holdout.
	Interventions int64 `json:"interventions"`
	Held          int64 `json:"held"`
	// RefusedRereads, RefusedTokens and RefusedUSD are the re-reads the
	// read guard refused, their size and their estimated value.
	RefusedRereads int64   `json:"refused_rereads"`
	RefusedTokens  int64   `json:"refused_tokens"`
	RefusedUSD     float64 `json:"refused_usd"`
	// HoldoutUSD is the saving the holdout comparison measured for the
	// day's tips; absent while no comparison has concluded.
	HoldoutUSD *float64 `json:"holdout_usd,omitempty"`
	// RealizedUSD is RefusedUSD and HoldoutUSD together: the potential
	// that was realized. Negative when the holdout measured that the tips
	// cost more than they saved.
	RealizedUSD float64 `json:"realized_usd"`
	// AvoidableUSD is the potential that was not: the day's waste at list
	// price.
	AvoidableUSD float64 `json:"avoidable_usd"`
}

// PlanAdvice is one provider's replayed tier fit (#653): the last Days of
// the plan's windows replayed against the other tiers of its family,
// metered overflow and the API at list price.
type PlanAdvice struct {
	Provider string `json:"provider"`
	// Plan is the plan in force.
	Plan string `json:"plan"`
	// Verdict is stay, switch or upgrade; Kind and RecommendedPlan the
	// option favoured (RecommendedPlan empty for the API).
	Verdict         string `json:"verdict"`
	Kind            string `json:"kind"`
	RecommendedPlan string `json:"recommended_plan,omitempty"`
	// MonthlyDeltaUSD is what the recommendation saves per month against
	// the plan in force; negative when it costs more (an upgrade).
	MonthlyDeltaUSD float64 `json:"monthly_delta_usd"`
	// Material is true when the saving is worth a switch.
	Material bool `json:"material"`
	// Lockouts and BlockedMinutes are the plan in force's, over the
	// period; RecommendedLockouts and RecommendedBlockedMinutes the
	// recommendation's.
	Lockouts                  int64 `json:"lockouts"`
	BlockedMinutes            int64 `json:"blocked_minutes"`
	RecommendedLockouts       int64 `json:"recommended_lockouts"`
	RecommendedBlockedMinutes int64 `json:"recommended_blocked_minutes"`
	// Confidence is low, medium or high.
	Confidence string `json:"confidence"`
	// Days is how much history the replay read; WindowsMeasured the plan
	// windows it replayed.
	Days            float64 `json:"days"`
	WindowsMeasured int64   `json:"windows_measured"`
}

// validateFinOps checks the schema 2 sections; covered are the upload's
// days.
func (u Upload) validateFinOps(covered map[string]bool) error {
	if u.Schema < SchemaFinOps {
		if len(u.PlanDays)+len(u.Waste)+len(u.Savings)+len(u.PlanAdvice) > 0 {
			return fmt.Errorf("schema %d carries no plan days, waste, savings or plan advice", u.Schema)
		}
		return nil
	}
	if err := checkRows("plan_days", u.PlanDays, MaxPlanDays, covered,
		func(r PlanDay) (string, string, error) {
			return r.Day, r.Day + "/" + r.Provider + "/" + r.Plan, r.Validate()
		}); err != nil {
		return err
	}
	if err := checkRows("waste", u.Waste, MaxWaste, covered,
		func(r Waste) (string, string, error) {
			return r.Day, r.Day + "/" + r.Repo + "/" + r.Kind + "/" + r.Class, r.Validate()
		}); err != nil {
		return err
	}
	if err := checkRows("savings", u.Savings, MaxDays, covered,
		func(r SavingsDay) (string, string, error) { return r.Day, r.Day, r.Validate() }); err != nil {
		return err
	}
	return checkRows("plan_advice", u.PlanAdvice, MaxPlanAdvice, nil,
		func(r PlanAdvice) (string, string, error) { return "", r.Provider, r.Validate() })
}

// checkRows validates each row, that its day is covered (when covered is
// set) and that no key appears twice.
func checkRows[T any](name string, rows []T, limit int, covered map[string]bool, check func(T) (day, key string, err error)) error {
	if len(rows) > limit {
		return fmt.Errorf("%s: at most %d per upload", name, limit)
	}
	seen := make(map[string]bool, len(rows))
	for i, r := range rows {
		day, key, err := check(r)
		if err != nil {
			return fmt.Errorf("%s[%d]: %w", name, i, err)
		}
		if covered != nil && !covered[day] {
			return fmt.Errorf("%s[%d]: day %s is not in days", name, i, day)
		}
		if seen[key] {
			return fmt.Errorf("%s[%d]: %s appears twice", name, i, key)
		}
		seen[key] = true
	}
	return nil
}

// Validate reports the first reason the row is malformed, or nil.
func (r PlanDay) Validate() error {
	if err := validDay(r.Day); err != nil {
		return err
	}
	if !ValidProvider(r.Provider) {
		return fmt.Errorf("provider %q: not a provider in the registry", r.Provider)
	}
	if !ValidPlan(r.Provider, r.Plan) {
		return fmt.Errorf("plan %q: not a catalog plan of %s", r.Plan, r.Provider)
	}
	if err := optionalPct("utilization_pct", r.UtilizationPct); err != nil {
		return err
	}
	if err := optionalPct("coverage_pct", r.CoveragePct); err != nil {
		return err
	}
	if err := nonNegative(map[string]int64{"utilization_windows": r.UtilizationWindows, "plan_tokens": r.PlanTokens,
		"billed_tokens": r.BilledTokens, "lockouts": r.Lockouts, "blocked_minutes": r.BlockedMinutes}); err != nil {
		return err
	}
	if r.BlockedMinutes > minutesPerDay {
		return errors.New("blocked_minutes: more than a day")
	}
	return nil
}

// Validate reports the first reason the row is malformed, or nil.
func (r Waste) Validate() error {
	if err := validDay(r.Day); err != nil {
		return err
	}
	if !ValidRepo(r.Repo) {
		return errors.New("repo: not a repository label (name or owner/name)")
	}
	if !ValidKind(r.Kind) {
		return fmt.Errorf("kind %q: not a kind of work", r.Kind)
	}
	if !ValidWasteClass(r.Class) {
		return fmt.Errorf("class %q: not a waste class", r.Class)
	}
	if err := nonNegative(map[string]int64{"turns": r.Turns, "tokens": r.Tokens}); err != nil {
		return err
	}
	return dollars(map[string]float64{"api_equivalent_usd": r.APIEquivalentUSD})
}

// Validate reports the first reason the row is malformed, or nil.
func (r SavingsDay) Validate() error {
	if err := validDay(r.Day); err != nil {
		return err
	}
	if err := nonNegative(map[string]int64{"interventions": r.Interventions, "held": r.Held,
		"refused_rereads": r.RefusedRereads, "refused_tokens": r.RefusedTokens}); err != nil {
		return err
	}
	if err := dollars(map[string]float64{"refused_usd": r.RefusedUSD, "avoidable_usd": r.AvoidableUSD}); err != nil {
		return err
	}
	// A holdout can measure that tips cost money: the realized figures
	// may be negative, never unbounded.
	if r.HoldoutUSD != nil && !bounded(*r.HoldoutUSD) {
		return errors.New("holdout_usd: not a bounded number")
	}
	if !bounded(r.RealizedUSD) {
		return errors.New("realized_usd: not a bounded number")
	}
	return nil
}

// Validate reports the first reason the row is malformed, or nil.
func (r PlanAdvice) Validate() error {
	if !ValidPlan(r.Provider, r.Plan) {
		return fmt.Errorf("%s/%s: not a catalog plan of a registered provider", r.Provider, r.Plan)
	}
	if !slices.Contains(AdviceVerdicts, r.Verdict) {
		return fmt.Errorf("verdict %q: not a verdict", r.Verdict)
	}
	if !slices.Contains(AdviceKinds, r.Kind) {
		return fmt.Errorf("kind %q: not an option kind", r.Kind)
	}
	switch {
	case r.Kind == "api" && r.RecommendedPlan != "":
		return errors.New("recommended_plan: the API option names no plan")
	case r.Kind != "api" && !ValidPlan(r.Provider, r.RecommendedPlan):
		return fmt.Errorf("recommended_plan %q: not a catalog plan of %s", r.RecommendedPlan, r.Provider)
	}
	if !slices.Contains(AdviceConfidences, r.Confidence) {
		return fmt.Errorf("confidence %q: not a confidence", r.Confidence)
	}
	if !bounded(r.MonthlyDeltaUSD) {
		return errors.New("monthly_delta_usd: not a bounded number")
	}
	if r.Days < 0 || r.Days > MaxAdviceDays || math.IsNaN(r.Days) {
		return fmt.Errorf("days: not between 0 and %d", MaxAdviceDays)
	}
	if err := nonNegative(map[string]int64{"lockouts": r.Lockouts, "blocked_minutes": r.BlockedMinutes,
		"recommended_lockouts": r.RecommendedLockouts, "recommended_blocked_minutes": r.RecommendedBlockedMinutes,
		"windows_measured": r.WindowsMeasured}); err != nil {
		return err
	}
	if limit := int64(MaxAdviceDays * minutesPerDay); r.BlockedMinutes > limit || r.RecommendedBlockedMinutes > limit {
		return errors.New("blocked minutes: longer than the period")
	}
	return nil
}

func validDay(d string) error {
	if _, err := time.Parse(DayLayout, d); err != nil {
		return fmt.Errorf("day %q is not a date", d)
	}
	return nil
}

func nonNegative(vs map[string]int64) error {
	for name, v := range vs {
		if v < 0 {
			return fmt.Errorf("%s: negative", name)
		}
	}
	return nil
}

func dollars(vs map[string]float64) error {
	for name, v := range vs {
		if v < 0 || !bounded(v) {
			return fmt.Errorf("%s: not a non-negative bounded number", name)
		}
	}
	return nil
}

func optionalPct(name string, v *float64) error {
	if v != nil && (*v < 0 || *v > 100 || math.IsNaN(*v)) {
		return fmt.Errorf("%s: not between 0 and 100", name)
	}
	return nil
}

// bounded reports whether v is a number no larger than maxUSD either way.
func bounded(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= maxUSD
}
