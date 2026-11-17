package daemon

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.klarlabs.de/tokenops/internal/capability/workquote"
	"go.klarlabs.de/tokenops/internal/contexts/governance/agentdx"
	"go.klarlabs.de/tokenops/internal/contexts/observability/analytics"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

type quoteLog struct{ pos sqlite.Position }

func (l quoteLog) Position(context.Context) (sqlite.Position, error) { return l.pos, nil }

// The runtime stores the basis after its delay.
func TestQuoteBasisRuntimeStoresTheBasis(t *testing.T) {
	dir := t.TempDir()
	start := time.Now().Add(-time.Hour)
	in := workquote.Inputs{
		Units: func(context.Context, time.Time, time.Time) ([]agentdx.Unit, error) {
			return []agentdx.Unit{{SessionID: "s", Harness: "claude-code", Model: "m", Prompt: "fix it", Turns: 1,
				Start: start, LastActivity: start.Add(time.Minute)}}, nil
		},
		Turns: func(context.Context, time.Time) ([]analytics.SessionTurn, error) {
			return []analytics.SessionTurn{{SessionID: "s", Provider: "anthropic", At: start.Add(30 * time.Second), APIEquivalentUSD: 1, Priced: true}}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runQuoteBasis(ctx, quoteLog{pos: sqlite.Position{Rowid: 1}}, in, dir, time.Millisecond, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, _ := workquote.Read(dir); b != nil {
			if len(b.Samples) != 1 {
				t.Errorf("basis = %+v", b)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no basis stored")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}
