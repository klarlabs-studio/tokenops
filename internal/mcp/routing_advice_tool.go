package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/decide"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// RoutingAdviceDeps wires the advisory routing tool.
type RoutingAdviceDeps struct {
	Config       *config.Config
	ConfigGetter func() *config.Config
	Store        *sqlite.Store
	// Spend prices the candidate models. serve passes the daemon's
	// engine, which carries the refreshed rate card; nil falls back to
	// the compiled-in table so zero-value deps stay valid.
	//
	// The tool used to build its own engine over the compiled-in table
	// while every other tool in the same server priced with the live
	// card, so "what the pricing table currently calls cheapest" was the
	// binary's opinion, not the table's.
	Spend *spend.Engine
}

func (d RoutingAdviceDeps) spendEngine() *spend.Engine {
	if d.Spend != nil {
		return d.Spend
	}
	return spend.NewEngine(spend.DefaultTable())
}

func (d RoutingAdviceDeps) activeConfig() *config.Config {
	if d.ConfigGetter != nil {
		return d.ConfigGetter()
	}
	return d.Config
}

type routingAdviceInput struct {
	Instruction string  `json:"instruction" jsonschema:"description=The operator's instruction you are about to act on, or the task you are about to hand a subagent. Pass it verbatim; it is classified locally and never stored."`
	Provider    string  `json:"provider,omitempty" jsonschema:"description=Provider name, e.g. anthropic. Defaults to the single configured plan when there is only one."`
	Model       string  `json:"model,omitempty" jsonschema:"description=The model this turn would otherwise run on. Without it there is nothing to compare against."`
	ToolDensity float64 `json:"tool_density,omitempty" jsonschema:"description=Share of the recent exchange that was tool traffic (0-1), when you can estimate it. Omitting it only makes the answer more conservative."`
	WorkID      string  `json:"work_id,omitempty" jsonschema:"description=Stable work identifier when the caller already knows it."`
	ExecutionID string  `json:"execution_id,omitempty" jsonschema:"description=Stable execution identifier when the caller already knows it."`
	ActorID     string  `json:"actor_id,omitempty" jsonschema:"description=Actor performing the execution when known."`
}

// routingAdviceResult is a recommendation, never an action. The
// distinction is the whole tool: nothing here changes which model
// anything runs on.
type routingAdviceResult struct {
	// Recommendation is "stay" or "switch".
	Recommendation string `json:"recommendation"`
	// Model is what to run on. Always populated — on "stay" it is the
	// model that was passed in, so a caller can use one field either way.
	Model string `json:"model"`
	// Reason names the signal the verdict was made from.
	Reason string `json:"reason"`
	// Class is what the turn was classified as: mechanical, reasoning, or
	// unknown when the classifier declined to call it.
	Class string `json:"class"`
	// WindowPct is how full the plan's rate-limit window is, and
	// WindowKnown whether that reading exists. An unmeasured window is
	// not a full one, and the policy treats it as a reason to stay.
	WindowPct   float64 `json:"window_pct,omitempty"`
	WindowKnown bool    `json:"window_known"`
	// Quality is the confidence attached to a switch.
	Quality float64 `json:"quality,omitempty"`
	// Note carries a setup problem that made the answer weaker than it
	// could be, rather than letting it read as a considered "stay".
	Note string `json:"note,omitempty"`
	// DecisionID joins this recommendation to its later action and outcome.
	DecisionID string `json:"decision_id,omitempty"`
	Stage      string `json:"stage,omitempty"`
	Authority  string `json:"authority,omitempty"`
	Executable bool   `json:"executable"`
	Recorded   bool   `json:"recorded"`
}

// RegisterRoutingAdviceTools exposes the routing decision as advice.
//
// Routing enforces by rewriting the model in the request, which needs the
// proxy, which needs a base-URL override most operators never wire. On
// those machines the optimizer holds an opinion nobody can hear. This is
// that same opinion, reachable by any agent that can call a tool — which
// is every client that speaks MCP, including the ones with no local
// transcripts and no proxy at all.
//
// It runs the same policy the request path runs, deliberately. Advice
// that disagreed with enforcement would be worse than no advice: an
// operator who followed it would be surprised twice.
//
// Nothing here applies anything. Choosing the model is the caller's, and
// ultimately the operator's, decision — this only makes the numbers
// behind it visible before it is made rather than after.
func RegisterRoutingAdviceTools(s *Server, d RoutingAdviceDeps) error {
	if s == nil {
		return errors.New("mcp: nil server")
	}
	s.Tool("tokenops_routing_advise").
		Description("Ask which model a turn should run on, decided from measured signal rather than a rule written once: what kind of work the instruction is (mechanical or reasoning), how full the plan's rate-limit window is, and what the pricing table currently calls cheapest. Call it before choosing a model for a turn or handing a task to a subagent. It only ever suggests routing DOWN, only for work it is confident is mechanical, and only while the window is genuinely tight — on a flat-rate plan a request costs nothing at the margin, so conserving while there is headroom trades quality for a saving that does not exist. It recommends and never applies; the model stays the caller's choice.").
		OutputSchema(routingAdviceResult{}).
		Handler(func(ctx context.Context, in routingAdviceInput) (*routingAdviceResult, error) {
			cfg := d.activeConfig()
			if cfg == nil {
				return &routingAdviceResult{
					Recommendation: "stay", Model: in.Model,
					Reason: "no configuration loaded, so there is nothing to decide from",
					Note:   "run `tokenops init` to create a config",
				}, nil
			}
			rc := cfg.Optimizer.RouterConfig()
			if rc == nil {
				return &routingAdviceResult{
					Recommendation: "stay", Model: in.Model,
					Reason: "no routing rules, and smart routing is off",
					Note:   "set `optimizer.smart_routing.enabled: true` to decide routes per turn without writing rules",
				}, nil
			}
			provider := resolveAdviceProvider(in.Provider, cfg)
			if provider == "" {
				return &routingAdviceResult{
					Recommendation: "stay", Model: in.Model,
					Reason: "no provider named and more than one is configured, so the window reading would be the wrong one",
					Note:   "pass provider explicitly",
				}, nil
			}

			pct, known := windowPressure(ctx, d.Store, cfg, provider)
			rc.WindowPressure = func(p eventschema.Provider) (float64, bool) {
				if p != provider {
					return 0, false
				}
				return pct, known
			}
			rc.PreferredModel = cfg.PreferredModel

			adv := router.New(*rc, d.spendEngine()).Advise(router.AdviceInput{
				Provider:    provider,
				Model:       in.Model,
				Instruction: in.Instruction,
				ToolDensity: in.ToolDensity,
			})

			out := &routingAdviceResult{
				Recommendation: "stay",
				Model:          in.Model,
				Reason:         adv.Reason,
				Class:          adv.Class,
				WindowPct:      adv.WindowPct,
				WindowKnown:    adv.WindowKnown,
			}
			if !adv.Stay && adv.Model != "" {
				out.Recommendation = "switch"
				out.Model = adv.Model
				out.Quality = adv.Quality
			}
			if !known {
				// Say why the answer is weaker than it looks. A "stay"
				// produced by a meter that is not reporting is not the
				// same answer as a "stay" produced by measured headroom.
				out.Note = "the plan's rate-limit window is not being measured, so conserving cannot be justified — check the plan binding (tokenops_plan_set, or `tokenops plan set`) and that the daemon is ingesting"
			}

			authority := decide.RoutingAuthority(*cfg)
			decision := decide.Route(decide.RouteInput{
				Provider: provider, CurrentModel: in.Model, Advice: adv,
				Authority: authority, Adapter: decide.Adapter{Name: "mcp", CanApplyRoute: false},
				Association: eventschema.Association{Work: in.WorkID, Execution: in.ExecutionID, Actor: in.ActorID},
				WindowKnown: known, WindowPct: pct, ObservedAt: time.Now().UTC(),
			})
			out.DecisionID = decision.DecisionID
			out.Stage = string(decision.Stage)
			out.Authority = authority.String()
			out.Executable = decision.Executable
			if d.Store != nil {
				if err := d.Store.Append(ctx, decision.Event); err != nil {
					return nil, err
				}
				out.Recorded = true
			} else {
				out.Note = appendNote(out.Note, "decision history is unavailable because storage is disabled")
			}
			return out, nil
		})
	return nil
}

func appendNote(current, next string) string {
	if current == "" {
		return next
	}
	return current + "; " + next
}

// resolveAdviceProvider takes the caller's provider, or infers it when
// exactly one plan is configured. With several plans it refuses to guess:
// the window reading is per provider, and answering from the wrong one is
// worse than asking.
func resolveAdviceProvider(named string, cfg *config.Config) eventschema.Provider {
	if named != "" {
		return eventschema.Provider(named)
	}
	if len(cfg.Plans) == 1 {
		for p := range cfg.Plans {
			return eventschema.Provider(p)
		}
	}
	return ""
}

// windowPressure reads how full a provider's rate-limit window is, from
// the same store and the same domain call the headroom tool uses.
//
// Unknown rather than zero on every failure. A router rule gated on
// window pressure must not fire on a default: this meter read 0/200 for
// months on a real machine, and acting on that would have degraded
// quality to relieve a shortage that was not happening.
func windowPressure(
	ctx context.Context,
	store *sqlite.Store,
	cfg *config.Config,
	provider eventschema.Provider,
) (float64, bool) {
	if store == nil || cfg == nil {
		return 0, false
	}
	planName, ok := cfg.Plans[string(provider)]
	if !ok {
		return 0, false
	}
	plan, ok := plans.Lookup(planName)
	if !ok || plan.RateLimitWindow <= 0 {
		return 0, false
	}
	reader := planStoreReader{store: store}
	now := time.Now().UTC()
	if a := plans.LatestAuthoritativeWindow(ctx, reader, provider, plan, now); a != nil {
		return a.UsedPct, true
	}
	if plan.MessagesPerWindow <= 0 {
		return 0, false
	}
	win, err := plans.ConsumptionInWindow(ctx, reader, string(provider), now, plan.RateLimitWindow)
	if err != nil {
		return 0, false
	}
	signal, err := classifySignalFromStore(ctx, store, string(provider), now.Add(-plan.RateLimitWindow), now)
	if err != nil {
		return 0, false
	}
	report, err := plans.ComputeHeadroom(planName, plans.HeadroomInputs{
		Now:            now,
		WindowMessages: win.MessagesInWindow,
		Signal:         signal,
	})
	if err != nil {
		return 0, false
	}
	return report.WindowPct, true
}
