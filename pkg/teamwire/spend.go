package teamwire

import (
	"errors"
	"fmt"
)

// The sections of a schema 3 upload (ADR 0016; ADR 0012 §11): spend by
// provider, for the organisation's FOCUS dataset; units of work, for unit
// economics across the organisation; and what the vendor billed, for
// reconciling the estimate with the invoice. Like every other section,
// each string is a date, a repository label or a provider from the
// registry, and each figure a sum or a bounded amount. No section names a
// model: the dataset is by provider.

// Limits on the schema 3 sections.
const (
	// MaxSpend bounds the spend rows: a bucket's worth, split by provider
	// rather than by kind of work.
	MaxSpend = MaxBuckets
	// MaxUnits bounds the unit rows: one per day and repository.
	MaxUnits = MaxBuckets
	// MaxVendorCosts bounds the vendor cost rows: a few providers a day.
	MaxVendorCosts = MaxDays * 8
)

// SpendBucket is what one machine's turns on one UTC day, in one
// repository, cost with one provider. A day's spend rows are the same
// turns as its buckets, split by provider rather than by kind of work.
type SpendBucket struct {
	Day      string `json:"day"`
	Repo     string `json:"repo"`
	Provider string `json:"provider"`
	// Turns and Tokens are what the turns used, input and output.
	Turns  int64 `json:"turns"`
	Tokens int64 `json:"tokens"`
	// CostUSD is what was billed, as TokenOps estimates it: nothing for
	// usage a plan covered. APIEquivalentUSD is the same work at list
	// prices, its value where a plan covers it.
	CostUSD          float64 `json:"cost_usd"`
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	// UnpricedTurns counts turns on models with no list price, left out
	// of the money.
	UnpricedTurns int64 `json:"unpriced_turns"`
}

// UnitBucket is the finished work one machine's member did on one UTC day
// in one repository, and the value of the work placed on it, by the
// client's own joins (`tokenops story --runtime`, `tokenops spend units`).
// A unit's value is zero when no work was placed on it; a value is never
// placed on no unit.
type UnitBucket struct {
	Day  string `json:"day"`
	Repo string `json:"repo"`
	// Tasks counts the inferred tasks that started that day and have
	// recorded usage; TaskUSD is their API-equivalent value.
	Tasks   int64   `json:"tasks"`
	TaskUSD float64 `json:"task_usd"`
	// Commits counts the member's commits; CommitUSD is the value of the
	// work placed on them.
	Commits   int64   `json:"commits"`
	CommitUSD float64 `json:"commit_usd"`
	// MergedPRs counts the member's pull requests merged into the default
	// branch; MergedPRUSD is the value of the work placed on them.
	MergedPRs   int64   `json:"merged_prs"`
	MergedPRUSD float64 `json:"merged_pr_usd"`
	// RevertedCommits counts the Commits a later revert of the member's
	// undid within 14 days, and RevertedCommitUSD the value placed on
	// them: the work that did not stand, the counterweight to the units.
	// Schema 4 only. A revert that lands after the day was uploaded counts
	// once the day is uploaded again.
	RevertedCommits   int64   `json:"reverted_commits,omitempty"`
	RevertedCommitUSD float64 `json:"reverted_commit_usd,omitempty"`
}

// VendorCost is what a vendor's own cost API reports the organisation was
// billed on one UTC day. Only a machine whose member is an owner or admin
// sends it, read with the admin key that stays on that machine; a server
// drops it from anyone else's upload (IngestResponse.VendorCostIgnored).
type VendorCost struct {
	Day      string  `json:"day"`
	Provider string  `json:"provider"`
	CostUSD  float64 `json:"cost_usd"`
}

// VendorCostRoles are the roles whose machines may send vendor cost.
var VendorCostRoles = []string{RoleOwner, RoleAdmin}

// validateSpend checks the schema 3 sections; covered are the upload's
// days.
func (u Upload) validateSpend(covered map[string]bool) error {
	if u.Schema < SchemaSpend {
		if len(u.Spend)+len(u.Units)+len(u.VendorCosts) > 0 {
			return fmt.Errorf("schema %d carries no spend, units or vendor cost", u.Schema)
		}
		return nil
	}
	if err := checkRows("spend", u.Spend, MaxSpend, covered,
		func(r SpendBucket) (string, string, error) {
			return r.Day, r.Day + "/" + r.Repo + "/" + r.Provider, r.Validate()
		}); err != nil {
		return err
	}
	if err := checkRows("units", u.Units, MaxUnits, covered,
		func(r UnitBucket) (string, string, error) { return r.Day, r.Day + "/" + r.Repo, r.Validate() }); err != nil {
		return err
	}
	if u.Schema < SchemaReverts {
		for _, r := range u.Units {
			if r.RevertedCommits != 0 || r.RevertedCommitUSD != 0 {
				return fmt.Errorf("units %s/%s: schema %d carries no reverted commits", r.Day, r.Repo, u.Schema)
			}
		}
	}
	return checkRows("vendor_cost", u.VendorCosts, MaxVendorCosts, covered,
		func(r VendorCost) (string, string, error) { return r.Day, r.Day + "/" + r.Provider, r.Validate() })
}

// Validate reports the first reason the row is malformed, or nil.
func (r SpendBucket) Validate() error {
	if err := validDay(r.Day); err != nil {
		return err
	}
	if !ValidRepo(r.Repo) {
		return errors.New("repo: not a repository label (name or owner/name)")
	}
	if !ValidProvider(r.Provider) {
		return fmt.Errorf("provider %q: not a provider in the registry", r.Provider)
	}
	if err := nonNegative(map[string]int64{"turns": r.Turns, "tokens": r.Tokens, "unpriced_turns": r.UnpricedTurns}); err != nil {
		return err
	}
	if r.UnpricedTurns > r.Turns {
		return errors.New("unpriced_turns: more than turns")
	}
	return dollars(map[string]float64{"cost_usd": r.CostUSD, "api_equivalent_usd": r.APIEquivalentUSD})
}

// Validate reports the first reason the row is malformed, or nil.
func (r UnitBucket) Validate() error {
	if err := validDay(r.Day); err != nil {
		return err
	}
	if !ValidRepo(r.Repo) {
		return errors.New("repo: not a repository label (name or owner/name)")
	}
	if err := nonNegative(map[string]int64{"tasks": r.Tasks, "commits": r.Commits, "merged_prs": r.MergedPRs,
		"reverted_commits": r.RevertedCommits}); err != nil {
		return err
	}
	if err := dollars(map[string]float64{"task_usd": r.TaskUSD, "commit_usd": r.CommitUSD,
		"merged_pr_usd": r.MergedPRUSD, "reverted_commit_usd": r.RevertedCommitUSD}); err != nil {
		return err
	}
	for name, unit := range map[string]struct {
		n int64
		v float64
	}{"task_usd": {r.Tasks, r.TaskUSD}, "commit_usd": {r.Commits, r.CommitUSD}, "merged_pr_usd": {r.MergedPRs, r.MergedPRUSD},
		"reverted_commit_usd": {r.RevertedCommits, r.RevertedCommitUSD}} {
		if unit.n == 0 && unit.v != 0 {
			return fmt.Errorf("%s: a value placed on no unit", name)
		}
	}
	if r.RevertedCommits > r.Commits {
		return errors.New("reverted_commits: more than commits")
	}
	if r.RevertedCommitUSD > r.CommitUSD*(1+1e-9)+1e-9 {
		return errors.New("reverted_commit_usd: more than commit_usd")
	}
	return nil
}

// Validate reports the first reason the row is malformed, or nil.
func (r VendorCost) Validate() error {
	if err := validDay(r.Day); err != nil {
		return err
	}
	if !ValidProvider(r.Provider) {
		return fmt.Errorf("provider %q: not a provider in the registry", r.Provider)
	}
	return dollars(map[string]float64{"cost_usd": r.CostUSD})
}
