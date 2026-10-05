package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/optimization/fmtlearn"
)

// The server orients an agent before its first call, naming the tools it
// will reach for first and the ones that need the operator's say.
func TestServerInstructions(t *testing.T) {
	got := NewServer("tokenops", "test", nil).Instructions()
	for _, want := range []string{"tokenops_glance", "tokenops_prepare_work", "tokenops_configure", "confirm with the operator"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions lack %q", want)
		}
	}
	for _, p := range publicTools() {
		if strings.Contains(got, "tokenops_") && !strings.Contains(got, p.name) && p.kind == changes {
			t.Errorf("instructions do not name %s, which changes settings", p.name)
		}
	}
}

// A summary with its figures underneath gives the figures back.
func TestJSONFromMarkdownPayload(t *testing.T) {
	raw, ok := jsonFromMarkdownPayload(markdownPayload("## Burn", map[string]any{"hours": 6}))
	var v map[string]any
	if !ok || json.Unmarshal(raw, &v) != nil || v["hours"] != float64(6) {
		t.Errorf("recovered %s %v", raw, ok)
	}
	if _, ok := jsonFromMarkdownPayload("just text"); ok {
		t.Error("plain text read as a payload")
	}
}

// fmt learn's lists are cut to the limit, with how many each had.
func TestTrimLearnReport(t *testing.T) {
	r := fmtlearn.Report{}
	for range 30 {
		r.Commands = append(r.Commands, fmtlearn.CommandStat{Command: "go"})
		r.NextFormatters = append(r.NextFormatters, fmtlearn.CommandStat{Command: "go"})
	}
	got := trimLearnReport(r, 0)
	if len(got.Commands) != defaultLearnLimit || got.CommandsTotal != 30 || got.NextFormattersTotal != 30 {
		t.Errorf("default trim: %d of %d", len(got.Commands), got.CommandsTotal)
	}
	if got := trimLearnReport(r, 5); len(got.NextFormatters) != 5 {
		t.Errorf("limit 5 kept %d", len(got.NextFormatters))
	}
	if got := trimLearnReport(fmtlearn.Report{}, 0); len(got.Commands) != 0 {
		t.Error("an empty report grew")
	}
}

// Through the public tools, burn comes back as figures rather than text.
func TestBurnIsStructured(t *testing.T) {
	srv := fullServer(t)
	if err := Consolidate(srv); err != nil {
		t.Fatal(err)
	}
	out, err := callPublic(t, srv, "tokenops_spend", map[string]any{"view": "burn"})
	if err != nil {
		t.Fatal(err)
	}
	burn, _ := out["burn"].(map[string]any)
	if _, isText := burn["text"]; isText || burn["hours"] == nil {
		t.Errorf("burn = %v", burn)
	}
}
