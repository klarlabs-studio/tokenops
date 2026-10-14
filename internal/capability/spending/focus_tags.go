package spending

import (
	"context"
	"sort"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/capability/commits"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

// taggedCharge is a charge, or the part of one, carrying allocation tags.
type taggedCharge struct {
	analytics.Charge
	Tags map[string]string
}

// chargeKey is the grain a charge is read at.
type chargeKey struct {
	day             int64
	provider, model string
	covered         bool
}

// tagPart sums the turns of one charge carrying one set of tags.
type tagPart struct {
	tags map[string]string
	analytics.Charge
}

// allocate tags the window's turns, when allocation rules are configured.
// Nil without rules: nothing is split.
func allocate(ctx context.Context, src ChargeReader, q FocusQuery, since, until time.Time) ([]allocation.TaggedTurn, error) {
	if q.Allocation == nil || len(q.Allocation.Rules) == 0 {
		return nil, nil
	}
	d := *q.Allocation
	if d.Turns == nil {
		r, ok := src.(commits.TurnReader)
		if !ok {
			return nil, nil
		}
		d.Turns = commits.TurnsIn(r, Window{IncludeSources: q.IncludeSources})
	}
	return allocation.Tagged(ctx, d, since, until)
}

// splitByTags splits each charge into the parts its tagged turns took and
// the rest. The parts add up to the charge: when no untagged turn is left,
// the last part absorbs what the turns and the charge differ by (a cost
// recomputed one way for the charge and another for the turn).
func splitByTags(charges []analytics.Charge, turns []allocation.TaggedTurn) []taggedCharge {
	parts := map[chargeKey]map[string]*tagPart{}
	for _, t := range turns {
		tags := t.Tags.Map()
		if tags == nil {
			continue
		}
		k := chargeKey{t.At.UTC().Truncate(24 * time.Hour).Unix(), t.Provider, t.Model, t.PlanCovered}
		if parts[k] == nil {
			parts[k] = map[string]*tagPart{}
		}
		id := tagID(tags)
		p := parts[k][id]
		if p == nil {
			p = &tagPart{tags: tags}
			parts[k][id] = p
		}
		p.Requests++
		p.InputTokens += t.InputTokens
		p.OutputTokens += t.OutputTokens
		p.ListUSD += t.APIEquivalentUSD
		if !t.PlanCovered {
			p.CostUSD += t.APIEquivalentUSD
		}
		p.Unpriced = p.Unpriced || !t.Priced
	}
	out := make([]taggedCharge, 0, len(charges))
	for _, c := range charges {
		byTag := parts[chargeKey{c.Day.UTC().Unix(), c.Provider, c.Model, c.PlanCovered}]
		if len(byTag) == 0 {
			out = append(out, taggedCharge{Charge: c})
			continue
		}
		ids := make([]string, 0, len(byTag))
		for id := range byTag {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		rest := c
		var split []taggedCharge
		for _, id := range ids {
			p := byTag[id]
			if p.Requests > rest.Requests {
				// The turns do not fit the charge (they were read over a
				// different filter); leave it whole rather than guess.
				split = nil
				break
			}
			part := c
			part.Requests, part.InputTokens, part.OutputTokens = p.Requests, p.InputTokens, p.OutputTokens
			part.CostUSD, part.ListUSD, part.Unpriced = p.CostUSD, p.ListUSD, p.Unpriced
			rest.Requests -= p.Requests
			rest.InputTokens -= p.InputTokens
			rest.OutputTokens -= p.OutputTokens
			rest.CostUSD -= p.CostUSD
			rest.ListUSD -= p.ListUSD
			split = append(split, taggedCharge{Charge: part, Tags: p.tags})
		}
		if split == nil {
			out = append(out, taggedCharge{Charge: c})
			continue
		}
		if rest.Requests > 0 {
			rest.InputTokens, rest.OutputTokens = max(rest.InputTokens, 0), max(rest.OutputTokens, 0)
			rest.CostUSD, rest.ListUSD = max(rest.CostUSD, 0), max(rest.ListUSD, 0)
			split = append(split, taggedCharge{Charge: rest})
		} else {
			last := &split[len(split)-1]
			last.InputTokens = max(last.InputTokens+rest.InputTokens, 0)
			last.OutputTokens = max(last.OutputTokens+rest.OutputTokens, 0)
			last.CostUSD = max(last.CostUSD+rest.CostUSD, 0)
			last.ListUSD = max(last.ListUSD+rest.ListUSD, 0)
		}
		out = append(out, split...)
	}
	return out
}

// tagID is a canonical key for a set of tags.
func tagID(tags map[string]string) string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(tags[k])
		b.WriteByte(0)
	}
	return b.String()
}
