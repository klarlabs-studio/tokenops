package routers

import (
	"context"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/decide"
	"go.klarlabs.de/tokenops/internal/capability/headroom"
	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer/router"
	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// AdviceDeps is what routing advice reads.
type AdviceDeps struct {
	// Config is the live configuration; nil answers "stay".
	Config *config.Config
	// Store backs the window reading and records the decision; nil
	// leaves the window unmeasured and the decision unrecorded.
	Store *sqlite.Store
	// Spend prices the candidate models; nil prices with the compiled-in
	// table.
	Spend *spend.Engine
}

func (d AdviceDeps) spend() *spend.Engine {
	if d.Spend != nil {
		return d.Spend
	}
	return spend.NewEngine(spend.DefaultTable())
}

// AdviceRequest is the turn to advise on.
type AdviceRequest struct {
	Instruction string
	Provider    string
	Model       string
	ToolDensity float64
	WorkID      string
	ExecutionID string
	ActorID     string
}

// Advice is a recommendation, never an action. The
// distinction is the whole tool: nothing here changes which model
// anything runs on.
type Advice struct {
	// Recommendation is "stay" or "switch".
	Recommendation string `json:"recommendation"`
	// DecidedBy names the external router that chooses this turn's
	// model, when one does; TokenOps then does not choose a second time
	// (ADR 0009 §5).
	DecidedBy string `json:"decided_by,omitempty"`
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

// Advise decides which model a turn should run on, from the same policy
// the proxy enforces, and records the decision. It recommends; nothing
// here changes which model anything runs on.
func Advise(ctx context.Context, d AdviceDeps, in AdviceRequest, now time.Time) (*Advice, error) {
	if r, ok := RouterNamedBy(in.Model); ok {
		return &Advice{
			Recommendation: "stay", Model: in.Model, DecidedBy: r.Name,
			Reason: r.Display + " chooses the model for each turn; TokenOps does not choose a second time, " +
				"because two routers for one turn cannot be explained",
		}, nil
	}
	cfg := d.Config
	if cfg == nil {
		return &Advice{
			Recommendation: "stay", Model: in.Model,
			Reason: "no configuration loaded, so there is nothing to decide from",
			Note:   "run `tokenops init` to create a config",
		}, nil
	}
	rc := cfg.RouterConfig()
	if rc == nil {
		return &Advice{
			Recommendation: "stay", Model: in.Model,
			Reason: "no routing rules, and smart routing is off",
			Note:   "set `optimizer.smart_routing.enabled: true` to decide routes per turn without writing rules",
		}, nil
	}
	provider := resolveAdviceProvider(in.Provider, cfg)
	if provider == "" {
		return &Advice{
			Recommendation: "stay", Model: in.Model,
			Reason: "no provider named and more than one is configured, so the window reading would be the wrong one",
			Note:   "pass provider explicitly",
		}, nil
	}

	pct, known := 0.0, false
	if d.Store != nil {
		pct, known = headroom.WindowPressure(ctx, cfg, d.Store, provider, now)
	}
	rc.WindowPressure = func(p eventschema.Provider) (float64, bool) {
		if p != provider {
			return 0, false
		}
		return pct, known
	}
	rc.PreferredModel = cfg.PreferredModel

	adv := router.New(*rc, d.spend()).Advise(router.AdviceInput{
		Provider: provider, Model: in.Model, Instruction: in.Instruction, ToolDensity: in.ToolDensity,
	})
	out := &Advice{
		Recommendation: "stay", Model: in.Model, Reason: adv.Reason, Class: adv.Class,
		WindowPct: adv.WindowPct, WindowKnown: adv.WindowKnown,
	}
	if !adv.Stay && adv.Model != "" {
		out.Recommendation, out.Model, out.Quality = "switch", adv.Model, adv.Quality
	}
	if !known {
		out.Note = "the plan's rate-limit window is not being measured, so conserving cannot be justified — check the plan binding (tokenops_configure (setting=plan), or `tokenops plan set`) and that the daemon is ingesting"
	}

	authority := decide.RoutingAuthority(*cfg)
	decision := decide.Route(decide.RouteInput{
		Provider: provider, CurrentModel: in.Model, Advice: adv,
		Authority: authority, Adapter: decide.Adapter{Name: "mcp", CanApplyRoute: false},
		Association: eventschema.Association{Work: in.WorkID, Execution: in.ExecutionID, Actor: in.ActorID},
		WindowKnown: known, WindowPct: pct, ObservedAt: now,
	})
	out.DecisionID, out.Stage, out.Authority = decision.DecisionID, string(decision.Stage), authority.String()
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
