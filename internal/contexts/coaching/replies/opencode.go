package replies

import (
	"errors"

	"go.klarlabs.de/tokenops/internal/contexts/telemetry/opencodedb"
)

// OpencodeDefaultPath returns opencode's store (opencodedb.DefaultPath).
func OpencodeDefaultPath() (string, error) { return opencodedb.DefaultPath() }

// errLimit stops a read once enough has been collected.
var errLimit = errors.New("replies: limit reached")

// ErrNoOpencodeReader reports an opencode extract with no store reader
// wired in ExtractOptions.Opencode: the store was not read, which is not the
// same as an operator who wrote nothing there.
var ErrNoOpencodeReader = errors.New("replies: no opencode reader configured (ExtractOptions.Opencode)")

// extractOpencode reads the model's replies out of opencode's store.
//
// Only text parts on assistant messages count. Reasoning parts are the
// model thinking rather than the prose the operator read, and this coach
// measures output density as experienced — counting reasoning would make
// every reply look far longer than what actually reached the screen.
func extractOpencode(path string, opts ExtractOptions) ([]AssistantReply, error) {
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
	var out []AssistantReply
	err := opts.Opencode.Read(path, opencodedb.Options{SessionID: opts.SessionID, Since: opts.Since, Parts: true}, func(m opencodedb.Message) error {
		if m.Role != opencodedb.Assistant || m.Created.IsZero() {
			return nil
		}
		if !opts.Until.IsZero() && m.Created.After(opts.Until) {
			return nil
		}
		// opencodedb keeps only text, never reasoning, so what remains is
		// what the operator read; each text part is one entry, as before.
		for _, text := range m.Text {
			out = append(out, AssistantReply{Timestamp: m.Created, SessionID: m.SessionID, Text: text})
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
