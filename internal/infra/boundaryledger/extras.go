package boundaryledger

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Extra is a temporary extra allowance the operator granted themselves:
// the named boundaries do not bind until Until, and then bind again on
// their own. It is the operator's word, given at a terminal; no agent
// surface can grant one.
type Extra struct {
	// ID is the decision the grant is recorded under (bnd-…), so explain
	// shows who granted it, when and why.
	ID string `json:"id"`
	// Rules are the boundaries it lifts.
	Rules []string  `json:"rules"`
	At    time.Time `json:"at"`
	Until time.Time `json:"until"`
	// Granter is the local account that granted it, Method how (cli),
	// and Why the reason they gave.
	Granter string `json:"granter"`
	Method  string `json:"method"`
	Why     string `json:"why,omitempty"`
	// EndedAt is set when the operator ended it before Until.
	EndedAt *time.Time `json:"ended_at,omitempty"`
}

// Active reports whether the extra lifts its boundaries at now.
func (e Extra) Active(now time.Time) bool {
	return e.EndedAt == nil && !now.Before(e.At) && now.Before(e.Until)
}

// Covers reports whether the extra lifts rule at now.
func (e Extra) Covers(rule string, now time.Time) bool {
	return e.Active(now) && slices.Contains(e.Rules, rule)
}

const extrasFile = "extras.json"

// extrasKeep is how long an ended or expired extra stays in the file, so
// status can say one just ended; the decision ledger keeps the record.
const extrasKeep = 7 * 24 * time.Hour

// Extras reads every extra granted within the last week, newest first.
func (d Dir) Extras() ([]Extra, error) {
	b, err := os.ReadFile(filepath.Join(d.root(), extrasFile)) //nolint:gosec // TokenOps' own state
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Extra
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ActiveExtras is the extras that lift boundaries at now. An unreadable
// file lifts nothing: a boundary binds unless the operator's grant is
// known.
func (d Dir) ActiveExtras(now time.Time) []Extra {
	all, err := d.Extras()
	if err != nil {
		return nil
	}
	var out []Extra
	for _, e := range all {
		if e.Active(now) {
			out = append(out, e)
		}
	}
	return out
}

// PutExtra records a grant, dropping extras that ended over a week ago.
func (d Dir) PutExtra(e Extra, now time.Time) error {
	all, err := d.Extras()
	if err != nil {
		// A corrupt file must not stop a grant; the ledger keeps history.
		all = nil
	}
	all = append(all, e)
	return d.writeExtras(all, now)
}

// EndExtras ends every extra active at now, and returns those it ended.
func (d Dir) EndExtras(now time.Time) ([]Extra, error) {
	all, err := d.Extras()
	if err != nil {
		return nil, err
	}
	var ended []Extra
	for i := range all {
		if all[i].Active(now) {
			at := now
			all[i].EndedAt = &at
			ended = append(ended, all[i])
		}
	}
	if len(ended) == 0 {
		return nil, nil
	}
	return ended, d.writeExtras(all, now)
}

func (d Dir) writeExtras(all []Extra, now time.Time) error {
	all = slices.DeleteFunc(all, func(e Extra) bool {
		end := e.Until
		if e.EndedAt != nil {
			end = *e.EndedAt
		}
		return now.Sub(end) > extrasKeep
	})
	slices.SortFunc(all, func(a, b Extra) int { return b.At.Compare(a.At) })
	b, err := json.Marshal(all)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.root(), 0o700); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(d.root(), extrasFile), b)
}
