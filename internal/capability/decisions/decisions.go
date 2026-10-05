// Package decisions answers what TokenOps decided and what waits on the
// operator: the routing proposals pending an answer, and why a decision was
// made. The MCP tools and the daemon API both call it (ADR 0010).
package decisions

import (
	"context"
	"fmt"
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/explain"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/routingapproval"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// Proposal is a model upgrade the proxy refused because it exceeds the
// preferred model, waiting on the operator.
type Proposal struct {
	Key            string `json:"key"`
	Provider       string `json:"provider"`
	RequestedModel string `json:"requested_model"`
	ProposedModel  string `json:"proposed_model"`
	PreferredModel string `json:"preferred_model"`
	TimesSeen      int64  `json:"times_seen"`
	Reason         string `json:"reason"`
	Question       string `json:"question"`
	// ExtraUSDPerMillion is what the upgrade adds per million input and
	// output tokens, when both models are priced.
	ExtraUSDPerMillion *float64 `json:"extra_usd_per_million_io_tokens,omitempty"`
	// Pricing explains a missing ExtraUSDPerMillion.
	Pricing string `json:"pricing,omitempty"`
}

// Proposals is everything waiting on the operator.
type Proposals struct {
	Pending []Proposal `json:"pending"`
	Note    string     `json:"note"`
}

// PendingProposals reads the approval store at path (empty: the default).
func PendingProposals(path string) (Proposals, error) {
	if path == "" {
		p, err := routingapproval.DefaultPath()
		if err != nil {
			return Proposals{}, err
		}
		path = p
	}
	store, err := routingapproval.Open(path)
	if err != nil {
		return Proposals{}, err
	}
	pending, err := store.Pending()
	if err != nil {
		return Proposals{}, err
	}
	out := Proposals{Pending: make([]Proposal, 0, len(pending))}
	if len(pending) == 0 {
		out.Note = "no upgrades are waiting on you"
		return out, nil
	}
	out.Note = "ask the operator before deciding; resolve with tokenops_routing (action=decide)"
	for _, p := range pending {
		pr := Proposal{
			Key: p.Key, Provider: p.Provider,
			RequestedModel: p.From, ProposedModel: p.To, PreferredModel: p.Preferred,
			TimesSeen: p.Seen, Reason: p.Reason,
			Question: fmt.Sprintf("Routing wants to switch %s → %s. Approve, or stay on your preferred %s?", p.From, p.To, p.Preferred),
		}
		if p.Priced {
			delta := p.DeltaUSD
			pr.ExtraUSDPerMillion = &delta
		} else {
			pr.Pricing = "unverifiable — no rate card for one of the models, so the route was refused rather than guessed at"
		}
		out.Pending = append(out.Pending, pr)
	}
	return out, nil
}

// Explanation is why a decision was made, or why it cannot be explained.
type Explanation struct {
	Report *explain.Report `json:"report,omitempty"`
	Error  string          `json:"error,omitempty"`
	Hint   string          `json:"hint,omitempty"`
}

// ErrMissingID is returned for an empty decision ID.
var ErrMissingID = fmt.Errorf("decision_id is required")

// Explain folds the stored records of decision id into its explanation.
// A nil store answers storage_disabled.
func Explain(ctx context.Context, store *sqlite.Store, id string) (Explanation, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Explanation{}, ErrMissingID
	}
	if store == nil {
		return Explanation{Error: "storage_disabled", Hint: "run `tokenops init` then restart the daemon"}, nil
	}
	events, err := store.Query(ctx, sqlite.Filter{Decision: id, Limit: 10_000})
	if err != nil {
		return Explanation{}, err
	}
	report, ok := explain.Build(id, events)
	if !ok {
		return Explanation{Error: "decision_not_found", Hint: "use the decision_id returned by tokenops_routing (action=advise) or a proxy intervention"}, nil
	}
	return Explanation{Report: &report}, nil
}
