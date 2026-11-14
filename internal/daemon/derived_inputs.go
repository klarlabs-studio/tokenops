package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"go.klarlabs.de/tokenops/internal/infra/observedlimits"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// pricingGeneration counts the rate cards a refresh applied to the running
// engine: a figure priced under one card is not the figure under the next.
var pricingGeneration atomic.Int64

// derivedInputs is where the inputs of a figure derived from the event
// log stand, other than the clock (ADR 0019): store's position, key (the
// config it is computed under), the rate card in force, and the size and
// modification time of each file in files it reads besides events.
// Without a store, or when its position cannot be read, they are unknown.
func derivedInputs(store *sqlite.Store, key string, files ...string) func(context.Context) (string, bool) {
	return func(ctx context.Context) (string, bool) {
		if store == nil {
			return "", false
		}
		pos, err := store.Position(ctx)
		if err != nil {
			return "", false
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s|%d|%d|%d", key, pos.Rowid, pos.Seq, pricingGeneration.Load())
		for _, f := range files {
			if st, err := os.Stat(f); err == nil {
				fmt.Fprintf(&b, "|%d.%d", st.Size(), st.ModTime().UnixNano())
			} else {
				b.WriteString("|-")
			}
		}
		return b.String(), true
	}
}

// planFiles are the files plan headroom reads besides events and config:
// the observed limits and the plan history. A file whose path cannot be
// told is left out; it is then read as missing on every computation.
func planFiles() []string {
	var out []string
	if f, err := observedlimits.Default(); err == nil {
		out = append(out, f.Path)
	}
	if f, err := planhistory.Default(); err == nil {
		out = append(out, f.Path)
	}
	return out
}
