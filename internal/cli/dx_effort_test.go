package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
)

// Where the rows carry a cost, each level shows what it cost beside its
// first-try success; where none does, the table keeps its old columns.
func TestWriteDXEffortShowsWhatEachLevelCost(t *testing.T) {
	rows := []sessions.EffortRow{
		{Model: "m", Effort: "high", Instructions: 40, Enough: true, FirstTryPct: 70, APIEquivalentUSD: 80, SharePct: 80, USDPerInstruction: 2},
		{Model: "m", Effort: "low", Instructions: 35, Enough: true, FirstTryPct: 64, APIEquivalentUSD: 20, SharePct: 20, USDPerInstruction: 0.57},
	}
	var b bytes.Buffer
	writeDXEffort(&b, rows, 7)
	out := b.String()
	for _, want := range []string{"COST", "SHARE", "$/INSTR", "$80.00", "80.0%", "$2.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	var plain bytes.Buffer
	writeDXEffort(&plain, []sessions.EffortRow{{Model: "m", Effort: "low", Instructions: 35, Enough: true}}, 7)
	if strings.Contains(plain.String(), "COST") {
		t.Errorf("cost columns without a cost:\n%s", plain.String())
	}
}

// Where a lower level did as well for materially less, dx suggests it
// beneath the table with the client's own setting, and says it leaves the
// setting alone.
func TestWriteDXEffortSuggestsALowerDefault(t *testing.T) {
	rows := []sessions.EffortRow{
		{Client: "claude-code", Model: "claude-opus-5-5", Effort: "medium", Instructions: 40, Enough: true, FirstTryPct: 70, APIEquivalentUSD: 16, USDPerInstruction: 0.4},
		{Client: "claude-code", Model: "claude-opus-5-5", Effort: "xhigh", Instructions: 120, Enough: true, FirstTryPct: 71, APIEquivalentUSD: 144, USDPerInstruction: 1.2},
	}
	var b bytes.Buffer
	writeDXEffort(&b, rows, 7)
	out := b.String()
	for _, want := range []string{"Try medium effort for claude-opus-5-5", "67% less", "estimate", `"effortLevel": "medium"`, "does not change it"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
