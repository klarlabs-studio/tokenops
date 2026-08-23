package readguard

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Reclamation is one re-read the guard actually prevented: tokens that
// were never spent.
//
// It exists so the saving can be published as an optimization event. The
// guard had been reclaiming hundreds of thousands of tokens while TEU —
// Token Efficiency Uplift — reported N/A, because TEU only ever counted
// optimizer events from the proxy and nothing told it that interventions
// were also happening inside the client.
type Reclamation struct {
	At        time.Time
	SessionID string
	AgentID   string
	Path      string
	Tokens    int64
}

// ID is a stable identity for one reclamation, so re-scanning the ledger
// republishes the same saving under the same key and the event store's
// dedup drops it instead of counting it twice.
func (r Reclamation) ID() string {
	h := sha256.Sum256(fmt.Appendf(nil, "readguard|%s|%s|%s|%s|%d",
		r.At.UTC().Format(time.RFC3339Nano), r.SessionID, r.AgentID, r.Path, r.Tokens))
	return "rgd-" + hex.EncodeToString(h[:8])
}

// Reclamations returns every read the guard blocked, in ledger order.
//
// Only ActionBlocked counts. An observe-mode would_block saved nothing —
// the read went ahead — and crediting it would report uplift the guard
// did not deliver, which is the failure this whole metric was rescued
// from.
//
// A missing ledger is an empty result: the daemon reads this before the
// guard has ever run. Malformed lines are skipped so a truncated final
// write cannot hide every saving before it.
func Reclamations(dir string) ([]Reclamation, error) {
	dir = resolveDir(dir)
	f, err := os.Open(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("readguard: open ledger: %w", err)
	}
	defer func() { _ = f.Close() }()

	var out []Reclamation
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var e ledgerEvent
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if e.Action != ActionBlocked || e.EstTokens <= 0 {
			continue
		}
		out = append(out, Reclamation{
			At:        e.TS.UTC(),
			SessionID: e.Session,
			AgentID:   e.Agent,
			Path:      e.Path,
			Tokens:    e.EstTokens,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("readguard: read ledger: %w", err)
	}
	return out, nil
}
