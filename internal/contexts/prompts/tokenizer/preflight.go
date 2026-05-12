package tokenizer

import (
	"github.com/felixgeelhaar/tokenops/pkg/eventschema"
)

// PreflightCount returns the estimated input token count for a request
// body. It first tries the structured ExtractMessages path; if extraction
// returns nothing (unknown shape, completion-style endpoints) it falls
// back to a raw-text count over body bytes interpreted as UTF-8.
//
// Returns 0 + ErrUnknownProvider when the registry has no tokenizer for p.
func (r *Registry) PreflightCount(p eventschema.Provider, body []byte) (int, error) {
	t, err := r.Lookup(p)
	if err != nil {
		return 0, err
	}
	if msgs, _ := ExtractMessages(p, body); len(msgs) > 0 {
		return t.CountMessages(msgs), nil
	}
	return t.CountText(string(body)), nil
}
