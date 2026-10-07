package sessions

import (
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/coaching/replies"
	opencodestore "go.klarlabs.de/tokenops/internal/infra/opencodedb"
)

// ReplyWindow selects which assistant replies to read.
type ReplyWindow struct {
	Root      string
	SessionID string
	Limit     int
	// Since is open when zero.
	Since time.Time
	// Source restricts the scan to one client; empty reads them all.
	Source string
}

// Replies is the reply coach's answer: how compressed the assistant's
// replies are, per session and overall.
type Replies = replies.Findings

// ReplyRecommendation is the one change the reply coach leads with.
type ReplyRecommendation = replies.Recommendation

// ReplyFindings measures the assistant replies in w for output
// compression (article and filler density, word length, code blocks).
// The text is read at scan time and never persisted.
func ReplyFindings(w ReplyWindow) (Replies, error) {
	extracted, err := replies.Extract(replies.ExtractOptions{
		Source: replies.Source(w.Source), Root: w.Root, SessionID: w.SessionID,
		Limit: w.Limit, Since: w.Since, Opencode: opencodestore.Store{},
	})
	if err != nil {
		return Replies{}, err
	}
	return replies.Analyze(extracted), nil
}

// RecommendReply is the biggest win in f, if there is one worth making.
func RecommendReply(f Replies) (ReplyRecommendation, bool) {
	return replies.Recommend(f)
}
