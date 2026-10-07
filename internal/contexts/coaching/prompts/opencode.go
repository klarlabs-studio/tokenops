package prompts

import (
	"errors"

	"go.klarlabs.de/tokenops/internal/contexts/telemetry/opencodedb"
)

// OpencodeDefaultPath returns opencode's store (opencodedb.DefaultPath).
func OpencodeDefaultPath() (string, error) { return opencodedb.DefaultPath() }

// errLimit stops a read once enough has been collected.
var errLimit = errors.New("prompts: limit reached")

// ErrNoOpencodeReader reports an opencode extract with no store reader
// wired in ExtractOptions.Opencode: the store was not read, which is not the
// same as an operator who wrote nothing there.
var ErrNoOpencodeReader = errors.New("prompts: no opencode reader configured (ExtractOptions.Opencode)")

// extractOpencode reads operator prompts out of opencode's SQLite store.
//
// opencode 1.x keeps the text on part rows and 2.x inline on the message;
// opencodedb reads both.
//
// opencode injects its own continuation prompts — "Continue if you have
// next steps" and similar — and marks them synthetic (2.x gives them a
// message type of their own). Those are the agent
// prodding itself, and coaching someone on words they never wrote would
// be worse than not coaching them at all, so they are dropped.
func extractOpencode(path string, opts ExtractOptions) ([]UserPrompt, error) {
	if opts.Opencode == nil {
		return nil, ErrNoOpencodeReader
	}
	if path == "" {
		p, err := OpencodeDefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	var out []UserPrompt
	err := opts.Opencode.Read(path, opencodedb.Options{SessionID: opts.SessionID, Since: opts.Since, Parts: true}, func(m opencodedb.Message) error {
		if m.Role != opencodedb.User || m.Created.IsZero() {
			return nil
		}
		if !opts.Until.IsZero() && m.Created.After(opts.Until) {
			return nil
		}
		// opencodedb leaves out synthetic text, so what remains is what
		// the operator typed; each text part is one entry, as before.
		for _, text := range m.Text {
			out = append(out, UserPrompt{Timestamp: m.Created, SessionID: m.SessionID, Text: text})
			if opts.Limit > 0 && len(out) >= opts.Limit {
				return errLimit
			}
		}
		return nil
	})
	if errors.Is(err, errLimit) {
		err = nil
	}
	return out, err
}
