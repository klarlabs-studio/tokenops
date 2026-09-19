package mcp

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/waste"
	"go.klarlabs.de/tokenops/internal/contexts/optimization/optimizer"
)

// serve used to build these from the config it started with, so an edit
// made mid-session — a context limit, a routing rule, a transcript root —
// did not reach the tool until the MCP client restarted the server. The
// call-time getter wins over the startup value.
func TestLiveDepsWinOverTheStartupValue(t *testing.T) {
	startup := waste.Config{}
	live := waste.Config{MaxContextTokens: 1}
	d := Deps{Waste: startup, WasteConfig: func() waste.Config { return live }}
	if got := d.wasteConfig(); got.MaxContextTokens != 1 {
		t.Errorf("waste config = %+v, want the live one", got)
	}

	livePipe := &optimizer.Pipeline{}
	p := ParityDeps{Pipeline: nil, PipelineFor: func() *optimizer.Pipeline { return livePipe }}
	if p.pipeline() != livePipe {
		t.Error("replay used the startup pipeline, not the live one")
	}

	c := CoachDeps{JSONLRoot: "/startup", RootFor: func() string { return "/live" }}
	if c.root() != "/live" {
		t.Errorf("coach root = %q, want /live", c.root())
	}

	// Without a getter the startup value still applies.
	if (CoachDeps{JSONLRoot: "/startup"}).root() != "/startup" {
		t.Error("static root ignored when no getter is wired")
	}
}
