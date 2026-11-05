package codexjsonl

import (
	"encoding/json"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/jsonltail"
)

// checkpointVersion names the saved format. Change it when readState or
// what a turn's ID is made of changes: the next start then reads every
// rollout once, as before checkpoints.
const checkpointVersion = 1

// checkpoint is what a restarted poller resumes from: how far it read each
// plain rollout, with the session state carried to the next line, and
// which compressed rollouts it read whole.
type checkpoint struct {
	Cursors []jsonltail.Saved[readState] `json:"cursors"`
	Packed  map[string]packedFile        `json:"packed,omitempty"`
}

type packedFile struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// restore resumes from the saved checkpoint, if there is one.
// Only once: Run may start again under the supervisor, and the live
// cursors are newer than the saved ones.
func (p *Poller) restore() {
	if p.ckpt == nil || p.restored {
		return
	}
	p.restored = true
	c, ok := p.ckpt.Load()
	if !ok {
		return
	}
	p.tail.Restore(c.Cursors)
	p.packed = make(map[string]fileIdentity, len(c.Packed))
	for path, f := range c.Packed {
		p.packed[path] = fileIdentity{size: f.Size, modTime: f.ModTime}
	}
	p.opts.Logger.Debug("codex jsonl: resumed from checkpoint", "files", len(c.Cursors), "compressed", len(c.Packed))
}

// checkpoint hands the positions after a scan to the checkpointer, which
// saves them once they are old enough to have been stored.
func (p *Poller) checkpoint() {
	if p.ckpt == nil {
		return
	}
	c := checkpoint{Cursors: p.tail.Checkpoint(), Packed: make(map[string]packedFile, len(p.packed))}
	for path, id := range p.packed {
		c.Packed[path] = packedFile{Size: id.size, ModTime: id.modTime}
	}
	if err := p.ckpt.Note(c); err != nil {
		p.opts.Logger.Warn("codex jsonl: checkpoint not saved; the next start reads more again", "err", err)
	}
}

// savedState is readState as a checkpoint holds it. The shape error is
// per read and not kept.
type savedState struct {
	SessionID     string `json:"session_id,omitempty"`
	ModelProvider string `json:"model_provider,omitempty"`
	Model         string `json:"model,omitempty"`
	Seq           int    `json:"seq"`
	Unrecognised  int    `json:"unrecognised,omitempty"`
}

func (st readState) MarshalJSON() ([]byte, error) {
	return json.Marshal(savedState{SessionID: st.sessionID, ModelProvider: st.modelProvider, Model: st.model,
		Seq: st.seq, Unrecognised: st.unrecognised})
}

func (st *readState) UnmarshalJSON(b []byte) error {
	var s savedState
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*st = readState{sessionID: s.SessionID, modelProvider: s.ModelProvider, model: s.Model, seq: s.Seq, unrecognised: s.Unrecognised}
	return nil
}
