package replies

import (
	"testing"
)

func TestAnalyze_EmptyInputReturnsZero(t *testing.T) {
	f := Analyze(nil)
	if f.TotalReplies != 0 || f.CavemanLikelySessions != 0 || len(f.BySession) != 0 {
		t.Errorf("expected zero findings on empty input; got %+v", f)
	}
}

func TestAnalyze_DetectsCavemanStyle(t *testing.T) {
	// Three short fragmented replies, no articles, no fillers, short words.
	rs := []AssistantReply{
		{SessionID: "s1", Text: "Bug in auth middleware. Fix `<` not `<=`. Ship."},
		{SessionID: "s1", Text: "Pool reuse open DB connections. Skip handshake overhead."},
		{SessionID: "s1", Text: "New object ref each render. Wrap in useMemo."},
	}
	f := Analyze(rs)
	if f.CavemanLikelySessions != 1 {
		t.Errorf("CavemanLikelySessions = %d, want 1", f.CavemanLikelySessions)
	}
	if len(f.BySession) != 1 || !f.BySession[0].CavemanLikely {
		t.Errorf("expected first session flagged caveman; got %+v", f.BySession)
	}
	if f.BySession[0].EstimatedSavedTokens <= 0 {
		t.Errorf("expected positive savings estimate; got %d", f.BySession[0].EstimatedSavedTokens)
	}
}

func TestAnalyze_DoesNotFlagVerboseBaseline(t *testing.T) {
	verbose := "Sure, I'd be happy to help with that. The issue you are " +
		"experiencing is actually really quite simple — basically, the " +
		"authentication middleware is checking the token expiry with the wrong " +
		"operator. We should perhaps consider updating the comparison to use " +
		"a less-than-or-equal-to check."
	rs := []AssistantReply{
		{SessionID: "s1", Text: verbose},
		{SessionID: "s1", Text: verbose},
		{SessionID: "s1", Text: verbose},
	}
	f := Analyze(rs)
	if f.CavemanLikelySessions != 0 {
		t.Errorf("verbose baseline should not be flagged caveman; got %+v", f.BySession)
	}
}

func TestAnalyze_SingleReplyTooNoisyToFlag(t *testing.T) {
	rs := []AssistantReply{
		{SessionID: "s1", Text: "Bug fix. Ship now."},
	}
	f := Analyze(rs)
	if f.CavemanLikelySessions != 0 {
		t.Errorf("single-reply session should not be flagged; got %+v", f.BySession)
	}
}

func TestAnalyze_GroupsSessionsAndOrdersByVerdict(t *testing.T) {
	rs := []AssistantReply{
		// verbose session
		{SessionID: "verbose", Text: "The fix is in the auth middleware. We are going to need to update the token expiry check operator."},
		{SessionID: "verbose", Text: "The pool will need to reuse the open database connections so that we can avoid the handshake overhead on each request."},
		{SessionID: "verbose", Text: "The new object reference is created on each render and that is why the component is re-rendering. We should wrap it in useMemo."},
		// caveman session
		{SessionID: "caveman", Text: "Bug in auth. Token expiry use `<` not `<=`. Ship."},
		{SessionID: "caveman", Text: "Pool reuse open DB conns. Skip handshake."},
		{SessionID: "caveman", Text: "New ref each render. Wrap in useMemo."},
	}
	f := Analyze(rs)
	if len(f.BySession) != 2 {
		t.Fatalf("expected 2 sessions; got %d", len(f.BySession))
	}
	if !f.BySession[0].CavemanLikely {
		t.Errorf("expected caveman session first; got %+v", f.BySession[0])
	}
	if f.BySession[1].CavemanLikely {
		t.Errorf("expected verbose session not flagged; got %+v", f.BySession[1])
	}
}

func TestSplitCodeBlocks_PreservesProseDropsFences(t *testing.T) {
	in := "Run this:\n```bash\nrm -rf /\n```\nDone."
	prose, had := splitCodeBlocks(in)
	if !had {
		t.Error("expected hadCode true")
	}
	if !contains(prose, "Run this:") || !contains(prose, "Done.") {
		t.Errorf("prose missing surrounding text: %q", prose)
	}
	if contains(prose, "rm -rf") {
		t.Errorf("prose should not include code-block contents: %q", prose)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
