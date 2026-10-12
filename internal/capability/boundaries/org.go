package boundaries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	domain "go.klarlabs.de/tokenops/internal/contexts/boundaries"
	"go.klarlabs.de/tokenops/internal/infra/boundaryledger"
	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/pkg/eventschema"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// Organisation rules (ADR 0014 §5). An owner or admin authors them in the
// team plane; the daemon fetches them with its upload cycle, as a signed
// document whose key this machine pinned on first fetch. Every read checks
// the signature again, so a rule edited or removed on disk is not a rule
// — a missing or broken document means no organisation rules, and status
// says so. They merge with local rules, where the strictest wins: a member
// can add boundaries but not loosen the organisation's.

// OrgSet is the organisation's rules this machine holds.
type OrgSet struct {
	Rules   []Rule
	Version int64
	// Problem says why the held rules could not be used; empty when they
	// verified (or there are none).
	Problem string
}

// LoadOrg reads and verifies the cached organisation rules against the
// key pinned in the team state at statePath. A machine that joined no
// team has none.
func LoadOrg(dir boundaryledger.Dir, statePath string) OrgSet {
	st, err := teamclient.Load(statePath)
	if err != nil {
		return OrgSet{}
	}
	raw, ok, err := dir.GetOrg()
	if err != nil || !ok {
		return OrgSet{}
	}
	var signed teamwire.SignedBoundaries
	if err := json.Unmarshal(raw, &signed); err != nil {
		return OrgSet{Problem: "the organisation's rules on disk are unreadable; the next fetch replaces them"}
	}
	if len(st.BoundaryKey) == 0 {
		return OrgSet{Problem: "the organisation's rules on disk were never verified; the next fetch replaces them"}
	}
	doc, err := signed.Verify(st.BoundaryKey)
	if err != nil {
		return OrgSet{Problem: err.Error()}
	}
	rules, err := doc.Parsed()
	if err != nil {
		return OrgSet{Version: doc.Version, Problem: "the organisation's rules do not parse: " + err.Error()}
	}
	return OrgSet{Rules: rules, Version: doc.Version}
}

// statePath is where cfg keeps the enrolment.
func statePath(cfg config.Config) string {
	p, err := teamclient.ResolveStatePath(cfg.Team.StatePath)
	if err != nil {
		return ""
	}
	return p
}

// AllRules are cfg's local rules and the organisation's.
func AllRules(cfg config.Config) []Rule {
	return append(cfg.BoundaryRules(), LoadOrg(boundaryledger.Dir(""), statePath(cfg)).Rules...)
}

// LiveRules is AllRules for a long-running process: the organisation's
// rules are read again whenever the daemon has fetched new ones, and only
// then.
func LiveRules(cfg config.Config) func() []Rule {
	local := cfg.BoundaryRules()
	path := statePath(cfg)
	dir := boundaryledger.Dir("")
	var (
		mu    sync.Mutex
		seen  time.Time
		org   []Rule
		ready bool
	)
	return func() []Rule {
		mu.Lock()
		defer mu.Unlock()
		if mod := dir.OrgModTime(); !ready || !mod.Equal(seen) {
			seen, ready = mod, true
			org = LoadOrg(dir, path).Rules
		}
		return append(append([]Rule(nil), local...), org...)
	}
}

// SyncOrg fetches the organisation's rules and keeps them when they verify:
// against the pinned key, or, on the first fetch, pinning the key they
// came with. An older version than the one held is ignored.
func SyncOrg(ctx context.Context, c *teamclient.Client, dir boundaryledger.Dir, statePath string) (int64, error) {
	st, err := teamclient.Load(statePath)
	if err != nil {
		return 0, err
	}
	signed, err := c.Boundaries(ctx)
	if err != nil {
		return 0, err
	}
	doc, err := signed.Verify(st.BoundaryKey)
	if err != nil {
		return 0, err
	}
	if _, err := doc.Parsed(); err != nil {
		return 0, fmt.Errorf("the organisation's rules do not parse here (update TokenOps?): %w", err)
	}
	if held := LoadOrg(dir, statePath); held.Problem == "" && held.Version > doc.Version {
		return held.Version, nil
	}
	if len(st.BoundaryKey) == 0 {
		st.BoundaryKey = signed.Key
		if err := teamclient.Save(statePath, st); err != nil {
			return 0, err
		}
	}
	raw, err := json.Marshal(signed)
	if err != nil {
		return 0, err
	}
	return doc.Version, dir.PutOrg(raw)
}

// teamRouted reports whether a pending decision waits on the organisation.
func teamRouted(p boundaryledger.Pending) bool {
	return strings.HasPrefix(p.Rule, domain.OrgPrefix) && strings.HasPrefix(p.Require, "approver:")
}

// approvalsTTL is how long a routed request waits before it is no longer
// asked about; the server expires it then too.
const approvalsTTL = 24 * time.Hour

// MethodTeam is an answer given in the team plane by the role a rule names.
const MethodTeam = "team"

// RouteApprovals sends each decision waiting on the organisation to the
// team plane once, and records the answers that came back. The answer is
// written where the pause point reads it, with who answered; the pending
// decision is then cleared, so the next time the rule holds it is asked
// afresh. version is the organisation rules' version held.
func RouteApprovals(ctx context.Context, c *teamclient.Client, env Env, version int64) error {
	pending, err := env.Dir.ListPending()
	if err != nil {
		return err
	}
	now := env.now()
	var errs []error
	for _, p := range pending {
		if !teamRouted(p) || now.Sub(p.At) > approvalsTTL {
			continue
		}
		if _, answered, _ := env.Dir.GetAnswer(p.ID, now); answered {
			continue
		}
		if p.RoutedAt == nil {
			if err := route(ctx, c, env, p, version, now); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		a, err := c.ApprovalFor(ctx, p.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := settle(env, p, a); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func route(ctx context.Context, c *teamclient.Client, env Env, p boundaryledger.Pending, version int64, now time.Time) error {
	req := teamwire.ApprovalRequest{
		DecisionID: p.ID, Rule: strings.TrimPrefix(p.Rule, domain.OrgPrefix), Version: max(version, 1),
		Action: p.Action, Harness: p.Harness, Crossings: wireCrossings(p.Crossings),
	}
	a, err := c.RequestApproval(ctx, req)
	if err != nil {
		return err
	}
	p.RoutedAt = &now
	if err := env.Dir.PutPending(p); err != nil {
		return err
	}
	return settle(env, p, a)
}

// settle records an answer the organisation gave; a request still pending
// or expired records nothing.
func settle(env Env, p boundaryledger.Pending, a teamwire.Approval) error {
	var approved bool
	switch a.Status {
	case teamwire.ApprovalApproved:
		approved = true
	case teamwire.ApprovalDenied:
	default:
		return nil
	}
	at := env.now()
	if a.AnsweredAt != nil {
		at = a.AnsweredAt.UTC()
	}
	scope := boundaryledger.ScopeOnce
	if a.Scope == teamwire.ScopeSession {
		scope = boundaryledger.ScopeSession
	}
	answer := boundaryledger.Answer{
		ID: p.ID, Rule: p.Rule, SessionID: p.SessionID, Approved: approved, Scope: scope,
		Approver: a.AnsweredBy, Method: MethodTeam, At: at,
	}
	if err := env.Dir.PutAnswer(answer); err != nil {
		return err
	}
	stage := eventschema.DecisionStageAccepted
	if !approved {
		stage = eventschema.DecisionStageRejected
	}
	if err := env.Dir.Append(answerEnvelope(p, stage, env, answer)); err != nil {
		return err
	}
	return env.Dir.DeletePending(p.ID)
}

func wireCrossings(in []domain.Crossing) []teamwire.Crossing {
	out := make([]teamwire.Crossing, 0, len(in))
	for _, c := range in {
		out = append(out, teamwire.Crossing{
			Field: string(c.Field), Op: string(c.Op), Value: c.Value, Threshold: c.Threshold,
			Window: string(c.Window), Destination: c.Destination,
		})
	}
	return out
}
