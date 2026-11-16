package teamwire

import (
	"fmt"
	"math"
	"slices"
	"sync"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// Schema 6 also splits the spend rows by model and agent (ADR 0012, Upload
// schema 6): the same turns, per day, repository, provider, model and the
// coding agent that recorded them. A model crosses only as an ID of the
// public pricing catalog this package is built with (the rate card
// TokenOps ships, internal/contexts/spend/spend/pricing.yaml), normalised
// to the row that prices it: "claude-opus-4-8-20261001" is sent as
// "claude-opus-4-8". Anything the catalog does not price — a custom or
// fine-tuned model, a router's or a local one, a name nobody knows — is
// sent as ModelOther, so a model's name can say nothing the catalog does
// not already publish. The agent is an enumeration.

// Vocabulary and limits of the model split.
const (
	// ModelOther is every model the catalog does not price.
	ModelOther = "other"
	// AgentOther is a turn recorded by no known coding agent: the proxy,
	// an SDK, a reader of another client.
	AgentOther = "other"
	// MaxModelSpend bounds the model rows. A day's rows split its spend
	// rows, so the bound is theirs.
	MaxModelSpend = MaxSpend
)

// Agents are the coding agents a turn can be filed under: the harnesses
// TokenOps reads sessions from (internal/capability/allocation, which a
// test in internal/capability/teamshare holds this list to), and
// AgentOther.
var Agents = []string{"claude-code", "codex", "opencode", "cursor", "gemini-cli", "pi", "copilot", AgentOther}

// ModelSpend is what one machine's turns on one UTC day, in one
// repository, with one provider, cost on one model through one agent. A
// day's model rows are its spend rows split further: for each day,
// repository and provider their turns, tokens, unpriced turns and dollars
// add up to the spend row's.
type ModelSpend struct {
	Day      string `json:"day"`
	Repo     string `json:"repo"`
	Provider string `json:"provider"`
	// Model is a catalog model ID (CatalogModel) or ModelOther.
	Model string `json:"model"`
	// Agent is one of Agents.
	Agent string `json:"agent"`
	// The figures, as on SpendBucket.
	Turns            int64   `json:"turns"`
	Tokens           int64   `json:"tokens"`
	CostUSD          float64 `json:"cost_usd"`
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	UnpricedTurns    int64   `json:"unpriced_turns"`
}

// catalog is the rate card this package is built with, parsed once.
var catalog = sync.OnceValue(spend.DefaultTable)

// CatalogModel is the ID under which provider's model may be uploaded: the
// catalog row that prices it, or ModelOther when none does. A dated or
// aliased ID is the row's ("gpt-4o-2024-08-06" is "gpt-4o"), and a model a
// reseller serves is its vendor's row.
func CatalogModel(provider, model string) string {
	k, ok := catalog().CatalogModel(eventschema.Provider(provider), model)
	if !ok {
		return ModelOther
	}
	return k.Model
}

// ValidModel reports whether model may be uploaded for provider: a
// registered provider, and ModelOther or a catalog ID that is its own
// normal form.
func ValidModel(provider, model string) bool {
	if !ValidProvider(provider) {
		return false
	}
	return model == ModelOther || CatalogModel(provider, model) == model
}

// ValidAgent reports whether s is one of Agents.
func ValidAgent(s string) bool { return slices.Contains(Agents, s) }

// Validate reports the first reason the row is malformed, or nil.
func (r ModelSpend) Validate() error {
	if err := (SpendBucket{Day: r.Day, Repo: r.Repo, Provider: r.Provider, Turns: r.Turns, Tokens: r.Tokens,
		CostUSD: r.CostUSD, APIEquivalentUSD: r.APIEquivalentUSD, UnpricedTurns: r.UnpricedTurns}).Validate(); err != nil {
		return err
	}
	if !ValidModel(r.Provider, r.Model) {
		return fmt.Errorf("model %q: not a catalog model of %s, nor %q", r.Model, r.Provider, ModelOther)
	}
	if !ValidAgent(r.Agent) {
		return fmt.Errorf("agent %q: not an agent", r.Agent)
	}
	return nil
}

// validateModelSpend checks the model rows: refused below schema 6, each
// row valid on a covered day and named once, and together a split of the
// spend rows, every one of which they cover.
func (u Upload) validateModelSpend(covered map[string]bool) error {
	if u.Schema < SchemaSeats {
		if len(u.SpendModels) > 0 {
			return fmt.Errorf("schema %d carries no spend by model", u.Schema)
		}
		return nil
	}
	if err := checkRows("spend_models", u.SpendModels, MaxModelSpend, covered, func(r ModelSpend) (string, string, error) {
		return r.Day, r.Day + "/" + r.Repo + "/" + r.Provider + "/" + r.Model + "/" + r.Agent, r.Validate()
	}); err != nil {
		return err
	}
	if len(u.SpendModels) == 0 {
		return nil
	}
	sums := map[string]*SpendBucket{}
	for _, r := range u.SpendModels {
		k := r.Day + "/" + r.Repo + "/" + r.Provider
		s := sums[k]
		if s == nil {
			s = &SpendBucket{}
			sums[k] = s
		}
		s.Turns += r.Turns
		s.Tokens += r.Tokens
		s.UnpricedTurns += r.UnpricedTurns
		s.CostUSD += r.CostUSD
		s.APIEquivalentUSD += r.APIEquivalentUSD
	}
	for _, s := range u.Spend {
		k := s.Day + "/" + s.Repo + "/" + s.Provider
		got := sums[k]
		if got == nil {
			return fmt.Errorf("spend_models: no rows for spend %s", k)
		}
		if got.Turns != s.Turns || got.Tokens != s.Tokens || got.UnpricedTurns != s.UnpricedTurns ||
			!sameDollars(got.CostUSD, s.CostUSD) || !sameDollars(got.APIEquivalentUSD, s.APIEquivalentUSD) {
			return fmt.Errorf("spend_models: the rows for %s do not add up to its spend row", k)
		}
		delete(sums, k)
	}
	for k := range sums {
		return fmt.Errorf("spend_models: %s has no spend row", k)
	}
	return nil
}

// sameDollars reports whether two sums of the same amounts, added in a
// different order, agree to well under a cent.
func sameDollars(a, b float64) bool {
	return math.Abs(a-b) <= 1e-6*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}
