package replies

import (
	"strings"
	"testing"
)

func sessionStats(id string, replies int, avgWords float64) SessionStat {
	return SessionStat{
		SessionID: id,
		Stats:     Stats{Replies: replies, AvgWords: avgWords, ArticleRatio: 0.09, FillerRatio: 0.006},
	}
}

// The reply coach measured verbosity and stopped there — a table of
// densities with no action, next to a prompt coach that converts patterns
// into turns, tokens, dollars and hours. This closes that gap.
func TestRecommendsAgainstTheOperatorsOwnLeanestSessions(t *testing.T) {
	f := Findings{
		TotalReplies: 1000,
		Baseline:     Stats{Replies: 1000, AvgWords: 60, ArticleRatio: 0.097, FillerRatio: 0.0064},
		BySession: []SessionStat{
			sessionStats("verbose", 400, 80),
			sessionStats("middling", 400, 60),
			sessionStats("lean", 200, 30),
		},
	}
	rec, ok := Recommend(f)
	if !ok {
		t.Fatal("expected a recommendation when sessions vary this much")
	}
	// The target is the operator's own leanest work, not an invented ideal.
	if rec.TargetAvgWords != 30 {
		t.Errorf("TargetAvgWords = %v, want 30 (their own leanest session)", rec.TargetAvgWords)
	}
	if rec.EstimatedTokensSaved <= 0 {
		t.Errorf("EstimatedTokensSaved = %d, want a positive projection", rec.EstimatedTokensSaved)
	}
	if !strings.Contains(rec.Action, "output") && !strings.Contains(rec.Action, "brev") {
		t.Errorf("Action should name a concrete change: %q", rec.Action)
	}
	if rec.Evidence == "" {
		t.Error("a recommendation must carry the evidence it rests on")
	}
}

// If the operator is already consistent there is nothing honest to
// recommend. Manufacturing advice from noise is how coaching loses trust.
func TestNoRecommendationWhenAlreadyConsistent(t *testing.T) {
	f := Findings{
		TotalReplies: 600,
		Baseline:     Stats{Replies: 600, AvgWords: 40},
		BySession: []SessionStat{
			sessionStats("a", 200, 39),
			sessionStats("b", 200, 40),
			sessionStats("c", 200, 41),
		},
	}
	if _, ok := Recommend(f); ok {
		t.Error("no recommendation should be made when sessions barely differ")
	}
}

// A session with a handful of replies is noise, not a target worth
// steering the whole workflow toward.
func TestIgnoresTinySessionsAsTargets(t *testing.T) {
	f := Findings{
		TotalReplies: 800,
		Baseline:     Stats{Replies: 800, AvgWords: 70},
		BySession: []SessionStat{
			sessionStats("real-a", 400, 72),
			sessionStats("real-b", 380, 68),
			sessionStats("blip", 2, 4),
		},
	}
	rec, ok := Recommend(f)
	if ok && rec.TargetAvgWords < 10 {
		t.Errorf("a 2-reply session must not set the target: %+v", rec)
	}
}

// Nothing to analyse yields nothing.
func TestNoRecommendationWithoutData(t *testing.T) {
	if _, ok := Recommend(Findings{}); ok {
		t.Error("empty findings must not produce a recommendation")
	}
}
