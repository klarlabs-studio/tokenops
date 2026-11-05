package claudecodejsonl

import (
	"encoding/json"
	"time"
)

// checkpointVersion names the saved format. Change it when readState
// changes: the next start then reads every transcript once, as before
// checkpoints.
const checkpointVersion = 1

// restore resumes from the saved checkpoint, if there is one.
// Only once: Run may start again under the supervisor, and the live
// cursors are newer than the saved ones.
func (p *Poller) restore() {
	if p.ckpt == nil || p.restored {
		return
	}
	p.restored = true
	saved, ok := p.ckpt.Load()
	if !ok {
		return
	}
	p.tail.Restore(saved)
	p.opts.Logger.Debug("claude-code jsonl: resumed from checkpoint", "files", len(saved))
}

// checkpoint hands the positions after a scan to the checkpointer, which
// saves them once they are old enough to have been stored.
func (p *Poller) checkpoint() {
	if p.ckpt == nil {
		return
	}
	if err := p.ckpt.Note(p.tail.Checkpoint()); err != nil {
		p.opts.Logger.Warn("claude-code jsonl: checkpoint not saved; the next start reads more again", "err", err)
	}
}

// savedState is readState as a checkpoint holds it.
type savedState struct {
	PendingUserMessage bool      `json:"pending_user_message,omitempty"`
	PrevAt             time.Time `json:"prev_at,omitzero"`
}

func (st readState) MarshalJSON() ([]byte, error) {
	return json.Marshal(savedState{PendingUserMessage: st.pendingUserMessage, PrevAt: st.prevAt})
}

func (st *readState) UnmarshalJSON(b []byte) error {
	var s savedState
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*st = readState{pendingUserMessage: s.PendingUserMessage, prevAt: s.PrevAt}
	return nil
}
