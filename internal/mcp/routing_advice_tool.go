package mcp

import (
	"context"
	"errors"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/routers"
	"go.klarlabs.de/tokenops/internal/capability/spending"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
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
	Spend *spending.Engine
}

func (d RoutingAdviceDeps) activeConfig() *config.Config {
	if d.ConfigGetter != nil {
		return d.ConfigGetter()
	}
	return d.Config
}

type routingAdviceInput struct {
	Instruction string  `json:"instruction" jsonschema:"required,description=The operator's instruction you are about to act on, or the task you are about to hand a subagent. Pass it verbatim; it is classified locally and never stored."`
	Provider    string  `json:"provider,omitempty" jsonschema:"description=Provider name, e.g. anthropic. Defaults to the single configured plan when there is only one."`
	Model       string  `json:"model,omitempty" jsonschema:"description=The model this turn would otherwise run on. Without it there is nothing to compare against."`
	ToolDensity float64 `json:"tool_density,omitempty" jsonschema:"description=Share of the recent exchange that was tool traffic (0-1), when you can estimate it. Omitting it only makes the answer more conservative."`
	WorkID      string  `json:"work_id,omitempty" jsonschema:"description=Stable work identifier when the caller already knows it."`
	ExecutionID string  `json:"execution_id,omitempty" jsonschema:"description=Stable execution identifier when the caller already knows it."`
	ActorID     string  `json:"actor_id,omitempty" jsonschema:"description=Actor performing the execution when known."`
	WorkflowID  string  `json:"workflow_id,omitempty" jsonschema:"description=Stable workflow identifier to preserve across prepare, execution attribution, and review. tokenops_prepare_work generates one when omitted."`
}

// routingAdviceResult is a recommendation, never an action.
type routingAdviceResult = routers.Advice

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
			return routingAdvice(ctx, in, d)
		})
	return registerWorkPreparationTool(s, d)
}

func routingAdvice(ctx context.Context, in routingAdviceInput, d RoutingAdviceDeps) (*routingAdviceResult, error) {
	return routers.Advise(ctx, routers.AdviceDeps{Config: d.activeConfig(), Store: d.Store, Spend: d.Spend}, routers.AdviceRequest{
		Instruction: in.Instruction, Provider: in.Provider, Model: in.Model, ToolDensity: in.ToolDensity,
		WorkID: in.WorkID, ExecutionID: in.ExecutionID, ActorID: in.ActorID,
	}, time.Now().UTC())
}
