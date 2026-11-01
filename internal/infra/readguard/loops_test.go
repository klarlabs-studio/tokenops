package readguard

import (
	"strings"
	"testing"
)

func always(string) bool { return true }

func read(path string) Call { return Call{Kind: CallRead, Path: path} }

func grep(pattern string) Call {
	return Call{Kind: CallSearch, Tool: "Grep", Pattern: pattern, Path: "internal"}
}

func edit() Call { return Call{Kind: CallEdit} }

func other(i int) Call { return read("/repo/other" + string(rune('a'+i)) + ".go") }

func TestObserveCallWindowing(t *testing.T) {
	tests := []struct {
		name  string
		calls []Call
		// want is the index of the call that should carry a tip, -1 for none.
		want int
		kind string
	}{
		{
			name:  "third read of a file within ten calls",
			calls: []Call{read("/repo/a.go"), other(0), read("/repo/a.go"), other(1), read("/repo/a.go")},
			want:  4, kind: TipRereadChurn,
		},
		{
			name:  "two reads are not churn",
			calls: []Call{read("/repo/a.go"), read("/repo/a.go")},
			want:  -1,
		},
		{
			name: "reads spread past the window are not churn",
			calls: []Call{read("/repo/a.go"), other(0), other(1), other(2), other(3), other(4),
				read("/repo/a.go"), other(5), other(6), other(7), other(8), other(9), read("/repo/a.go")},
			want: -1,
		},
		{
			name:  "an edit between reads does not reset churn",
			calls: []Call{read("/repo/a.go"), edit(), read("/repo/a.go"), edit(), read("/repo/a.go")},
			want:  4, kind: TipRereadChurn,
		},
		{
			name: "different ranges of one file are not churn",
			calls: []Call{
				{Kind: CallRead, Path: "/repo/big.go", Range: "0:100"},
				{Kind: CallRead, Path: "/repo/big.go", Range: "100:100"},
				{Kind: CallRead, Path: "/repo/big.go", Range: "200:100"},
			},
			want: -1,
		},
		{
			name:  "third identical search without an edit",
			calls: []Call{grep("Foo"), read("/repo/a.go"), grep("Foo "), other(1), grep(" Foo")},
			want:  4, kind: TipSearchLoop,
		},
		{
			name:  "an edit resets the search loop",
			calls: []Call{grep("Foo"), grep("Foo"), edit(), grep("Foo")},
			want:  -1,
		},
		{
			name:  "a different path is a different search",
			calls: []Call{grep("Foo"), grep("Foo"), {Kind: CallSearch, Tool: "Grep", Pattern: "Foo", Path: "cmd"}},
			want:  -1,
		},
		{
			name:  "Glob and Grep with one pattern are different searches",
			calls: []Call{grep("Foo"), grep("Foo"), {Kind: CallSearch, Tool: "Glob", Pattern: "Foo", Path: "internal"}},
			want:  -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			got := -1
			for i, c := range tt.calls {
				tip, ok := ObserveCall(dir, "s1", "", c, always)
				if ok {
					if got != -1 {
						t.Fatalf("a second tip at call %d (first at %d)", i, got)
					}
					got = i
					if tip.Kind != tt.kind {
						t.Errorf("kind = %q, want %q", tip.Kind, tt.kind)
					}
				}
			}
			if got != tt.want {
				t.Fatalf("tip at call %d, want %d", got, tt.want)
			}
		})
	}
}

func TestObserveCallTellsOncePerSession(t *testing.T) {
	dir := t.TempDir()
	tips := 0
	for range 9 {
		if _, ok := ObserveCall(dir, "s1", "", read("/repo/a.go"), always); ok {
			tips++
		}
	}
	if tips != 1 {
		t.Fatalf("told %d times about one file, want once", tips)
	}
	// Another session, or a subagent's own context, starts afresh.
	for _, scope := range []struct{ session, agent string }{{"s2", ""}, {"s1", "sub"}} {
		n := 0
		for range 3 {
			if _, ok := ObserveCall(dir, scope.session, scope.agent, read("/repo/a.go"), always); ok {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%+v: told %d times, want once", scope, n)
		}
	}
}

func TestObserveCallDeclinedTipIsNotSpent(t *testing.T) {
	dir := t.TempDir()
	asked := 0
	never := func(string) bool { asked++; return false }
	for range 3 {
		if _, ok := ObserveCall(dir, "s1", "", grep("Foo"), never); ok {
			t.Fatal("spoke while the coach may not")
		}
	}
	if asked != 1 {
		t.Fatalf("asked %d times, want once (only when a tip is due)", asked)
	}
	if _, ok := ObserveCall(dir, "s1", "", grep("Foo"), always); !ok {
		t.Fatal("a declined tip was marked as given")
	}
	if _, ok := ObserveCall(dir, "s1", "", grep("Foo"), nil); ok {
		t.Fatal("a nil speak spoke")
	}
}

func TestLoopTipMessages(t *testing.T) {
	dir := t.TempDir()
	var tip LoopTip
	for range 3 {
		tip, _ = ObserveCall(dir, "s1", "", Call{Kind: CallRead, Path: "/repo/internal/cli/readguard.go", Cwd: "/repo"}, always)
	}
	msg := tip.Message()
	if !strings.Contains(msg, "readguard.go (internal/cli/readguard.go)") || !strings.Contains(msg, "3 times in the last 10") {
		t.Errorf("re-read note = %q", msg)
	}

	long := strings.Repeat("x", 200) + "\nsecret line"
	for range 3 {
		tip, _ = ObserveCall(dir, "s1", "", Call{Kind: CallSearch, Tool: "Grep", Pattern: long, Path: "/repo/internal", Cwd: "/repo"}, always)
	}
	msg = tip.Message()
	if !strings.Contains(msg, `Grep for "`+strings.Repeat("x", maxPatternRunes)+`…" in internal`) {
		t.Errorf("search note = %q", msg)
	}
	if strings.Contains(msg, "secret") || strings.Contains(msg, "\n") {
		t.Errorf("search note quotes past the cut: %q", msg)
	}
}

func TestLoopStateHoldsNoPaths(t *testing.T) {
	dir := t.TempDir()
	ObserveCall(dir, "s1", "", Call{Kind: CallSearch, Tool: "Grep", Pattern: "needle", Path: "/repo/hay"}, always)
	b := loadLoops(loopFile(dir, "s1"))
	for _, e := range b.Recent {
		if strings.Contains(e.Key, "needle") || strings.Contains(e.Key, "hay") {
			t.Fatalf("state keeps the search in clear: %+v", e)
		}
	}
}

// A loop tip is settled by what the agent does next: the loop recurring
// within SettleAfter calls is ignoring it, with what it did instead; an
// edit after a search loop, or SettleAfter calls without the loop, is
// following it.
func TestObserveSettlesTips(t *testing.T) {
	speak := func(LoopTip) bool { return true }
	ids := 0
	newID := func() string { ids++; return "tip" + string(rune('0'+ids)) }
	loop := func(c Call) []Call { return []Call{c, c, c} }
	cases := []struct {
		name     string
		calls    []Call
		after    []Call
		followed bool
		instead  string
		settled  bool
	}{
		{"the same file read again is ignored", loop(read("/repo/a.go")), []Call{read("/repo/a.go")}, false, "read a.go again", true},
		{"the same file in another range is still ignored", loop(read("/repo/a.go")),
			[]Call{{Kind: CallRead, Path: "/repo/a.go", Range: "10:20"}}, false, "read a.go again", true},
		{"no re-read in the next calls is followed", loop(read("/repo/a.go")),
			[]Call{other(0), other(1), other(2), other(3), other(4)}, true, "", true},
		{"fewer calls than the wait leave it open", loop(read("/repo/a.go")), []Call{other(0), other(1)}, false, "", false},
		{"the same search again is ignored", loop(grep("Foo")), []Call{grep("Foo")}, false, "ran the same Grep again", true},
		{"an edit after a search loop is followed", loop(grep("Foo")), []Call{edit()}, true, "", true},
		{"another search is not the loop", loop(grep("Foo")), []Call{grep("Bar")}, false, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			var given string
			for _, call := range c.calls {
				if o := Observe(dir, "s1", "", call, speak, newID); o.Spoke {
					given = o.ID
				}
			}
			if given == "" {
				t.Fatal("no tip given")
			}
			var settled []TipOutcome
			for _, call := range c.after {
				settled = append(settled, Observe(dir, "s1", "", call, speak, newID).Settled...)
			}
			if !c.settled {
				if len(settled) != 0 {
					t.Fatalf("settled %+v; want it open", settled)
				}
				return
			}
			if len(settled) != 1 || settled[0].ID != given || settled[0].Followed != c.followed || settled[0].Instead != c.instead {
				t.Fatalf("settled %+v; want %s followed=%v instead %q", settled, given, c.followed, c.instead)
			}
		})
	}
}

func TestLoopTipObjectNamesOneThing(t *testing.T) {
	dir := t.TempDir()
	var tip LoopTip
	for range 3 {
		tip, _ = ObserveCall(dir, "s1", "", Call{Kind: CallRead, Path: "/repo/internal/cli/readguard.go", Range: "1:5"}, always)
	}
	if tip.Object() != "readguard.go" {
		t.Fatalf("read object = %q", tip.Object())
	}
	for range 3 {
		tip, _ = ObserveCall(dir, "s2", "", grep("needle"), always)
	}
	if tip.Object() != `Grep "needle"` {
		t.Fatalf("search object = %q", tip.Object())
	}
}
