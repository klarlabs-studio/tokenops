package attribution

import (
	"context"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl"
	"go.klarlabs.de/tokenops/internal/storage/sqlite"
)

// codexOutputBatch is how many Codex turns one write of the correction
// reads: a batch holds the write lock other processes wait on for well
// under their busy timeout.
const codexOutputBatch = 1000

// CodexOutputReason is why CorrectCodexOutput changed recorded turns, as
// the audit log records it.
const CodexOutputReason = "Codex turns counted their reasoning tokens twice in output and total"

// CorrectCodexOutput takes the reasoning tokens that Codex turns stored
// by releases up to v0.118.18 counted twice back out of their output and
// total (sqlite.Store.CorrectCodexOutput), audits how many it corrected,
// and returns that count. It is meant for every daemon start: a store
// with nothing left to correct is only read. What a pass cut short by
// shutdown corrected is still audited, and the next pass carries on.
func CorrectCodexOutput(ctx context.Context, store *sqlite.Store) (int64, error) {
	n, err := store.CorrectCodexOutput(ctx, codexOutputBatch)
	if n > 0 {
		record(context.WithoutCancel(ctx), store, "codex", map[string]any{
			"source": codexjsonl.SourceTag, "calls": n, "reason": CodexOutputReason,
		})
	}
	return n, err
}
