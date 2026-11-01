package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	coachcap "go.klarlabs.de/tokenops/internal/capability/coach"
	"go.klarlabs.de/tokenops/internal/contexts/coaching/moveverdict"
	"go.klarlabs.de/tokenops/internal/infra/claudelimits"
	"go.klarlabs.de/tokenops/internal/infra/moveverify"
)

func runRoot(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	root := NewRoot()
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&out)
	if err := root.Execute(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

// `tokenops explain moves` says, per move kind, what was judged and why a
// move was retracted, and `tokenops coach reinstate` restores it.
func TestExplainMovesAndReinstate(t *testing.T) {
	store := coachcap.MoveStore()
	_ = os.RemoveAll(store.Dir)
	t.Cleanup(func() { _ = os.RemoveAll(store.Dir) })
	if out := runRoot(t, "explain", "moves"); !strings.Contains(out, "No autonomous model moves to judge yet") {
		t.Fatalf("empty explain = %q", out)
	}
	k := moveverdict.Key{Kind: "edit", From: "opus", To: "sonnet"}
	ev := moveverdict.Belief{Key: k, Moves: 10, Judged: 10, Bad: 6, Baseline: 10, BaselineBad: 1, Rate: 0.6, BaselineRate: 0.1,
		Verdict: moveverdict.VerdictDropped, Why: "subagent runs went wrong 60% of the time on sonnet against 10% on opus"}
	if err := store.Update(func(st *moveverify.State) error {
		st.Retractions = []moveverdict.Retraction{{ID: "d1", Key: k, At: time.Now(), Reason: ev.Why, Evidence: ev}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out := runRoot(t, "explain", "moves")
	for _, want := range []string{"edit work, opus → sonnet: 10 moves, 10 judged against 10 comparable runs", "quality dropped", "retracted", "tokenops coach reinstate edit"} {
		if !strings.Contains(out, want) {
			t.Errorf("explain moves lacks %q:\n%s", want, out)
		}
	}
	if out := runRoot(t, "coach", "reinstate", "edit"); !strings.Contains(out, "Reinstated opus → sonnet for edit (decision d1)") {
		t.Fatalf("reinstate = %q", out)
	}
	if store.Retracted()("edit", "opus", "sonnet") {
		t.Fatal("still retracted after reinstate")
	}
	if out := runRoot(t, "coach", "reinstate", "deep"); !strings.Contains(out, "No retracted move") {
		t.Fatalf("reinstate of nothing = %q", out)
	}
}

// `tokenops explain price` names λ, the window it comes from, projected
// against remaining, and why each kind of work was or was not escalated.
func TestExplainPrice(t *testing.T) {
	_ = os.Remove(claudelimits.DefaultPath())
	if out := runRoot(t, "explain", "price"); !strings.Contains(out, "No price of quota") {
		t.Fatalf("no reading = %q", out)
	}
	writeCoachConfig(t, fourTiers)
	writePressedWeek(t)
	out := runRoot(t, "explain", "price")
	for _, want := range []string{
		"Price of quota (λ): 0.74, from the Claude weekly window",
		"wants ~212% of the plan; 54% is left",
		"edit work not escalated to claude-opus-5",
		"deep work not escalated to claude-fable-5",
		"never a cap",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("explain price lacks %q:\n%s", want, out)
		}
	}
	var v quotaPriceView
	if err := json.Unmarshal([]byte(runRoot(t, "explain", "price", "--json")), &v); err != nil || v.Price == nil || v.Price.Lambda < 0.7 || len(v.Escalations) != 2 {
		t.Fatalf("json = %+v %v", v, err)
	}
}
