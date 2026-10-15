package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/allocation"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
)

func TestWriteTagReport(t *testing.T) {
	t0 := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	turn := func(session string, usd float64, tags allocation.Tags, known bool) allocation.TaggedTurn {
		return allocation.TaggedTurn{SessionTurn: analytics.SessionTurn{SessionID: session, At: t0, Tokens: 1500,
			APIEquivalentUSD: usd, Priced: true}, Tags: tags, DirKnown: known}
	}
	r := allocation.ByKey(allocation.Client, []allocation.TaggedTurn{
		turn("a", 12, allocation.Tags{Client: "Acme Corp"}, true),
		turn("b", 3, allocation.Tags{Client: "globex"}, true),
		turn("c", 5, allocation.Tags{}, false),
	})
	r.Since, r.Rules = t0, 2
	var b bytes.Buffer
	writeTagReport(&b, r)
	out := b.String()
	for _, want := range []string{
		"Spend by client since", "75% of the window's work carries a client",
		"globex", "(untagged)", "$5.00 of it from sessions whose directory no client recorded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(out), " "), "Acme Corp $12.00 $12.00 1 1 2k") {
		t.Errorf("the Acme Corp row is wrong:\n%s", out)
	}
	if strings.Index(out, "Acme Corp") > strings.Index(out, "globex") {
		t.Error("the most valuable value is not first")
	}

	b.Reset()
	writeTagReport(&b, allocation.Report{Key: allocation.Project, Since: t0})
	if !strings.Contains(b.String(), "No allocation rules are configured") {
		t.Errorf("without rules:\n%s", b.String())
	}
}

func TestAllocationKeyReadsSpendBy(t *testing.T) {
	for _, by := range []string{"project", "client", "cost_center", "cost-center"} {
		if _, ok := allocationKey(by); !ok {
			t.Errorf("--by %s is not an allocation key", by)
		}
	}
	for _, by := range []string{"model", "commit", "agent"} {
		if _, ok := allocationKey(by); ok {
			t.Errorf("--by %s read as an allocation key", by)
		}
	}
}
