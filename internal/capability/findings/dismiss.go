package findings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/jsonfile"
)

// Dismissal hides one finding, by key, until a time, while it is no more
// urgent than when it was dismissed: a finding that rises to a higher
// level shows again.
type Dismissal struct {
	Key   string    `json:"key"`
	Level string    `json:"level"`
	Until time.Time `json:"until"`
}

// urgency ranks levels, most urgent lowest.
var urgency = map[string]int{LevelWarn: 0, LevelNotice: 1, LevelInfo: 2}

// hides reports whether d hides f at now.
func (d Dismissal) hides(f Finding, now time.Time) bool {
	return d.Key == f.Key && now.Before(d.Until) && urgency[f.Level] >= urgency[d.Level]
}

// Filter leaves out of r the findings ds hide at now and counts them in
// Hidden. r is not changed.
func Filter(r Report, ds []Dismissal, now time.Time) Report {
	out := r
	out.Findings = make([]Finding, 0, len(r.Findings))
	out.Hidden = 0
	for _, f := range r.Findings {
		hidden := false
		for _, d := range ds {
			if d.hides(f, now) {
				hidden = true
				break
			}
		}
		if hidden {
			out.Hidden++
			continue
		}
		out.Findings = append(out.Findings, f)
	}
	return out
}

// DismissFor is how long a dismissal named by word lasts: a day or a week.
func DismissFor(word string) (time.Duration, bool) {
	switch word {
	case "day":
		return 24 * time.Hour, true
	case "week":
		return 7 * 24 * time.Hour, true
	}
	return 0, false
}

// dismissedFile names the dismissals under the TokenOps directory.
const dismissedFile = "findings-dismissed.json"

// DefaultDismissals is the store at ~/.tokenops/findings-dismissed.json.
func DefaultDismissals() Dismissals {
	return Dismissals{Path: filepath.Join(filepath.Dir(DefaultDir()), dismissedFile)}
}

// Dismissals is the file of findings the operator hid. It holds keys,
// levels and times, nothing more.
type Dismissals struct {
	Path string
}

// Load reads the dismissals still in force at now. A damaged file is an
// error: the caller shows every finding rather than guess.
func (d Dismissals) Load(now time.Time) ([]Dismissal, error) {
	b, err := os.ReadFile(d.Path) //nolint:gosec // TokenOps' own state file
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all []Dismissal
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, err
	}
	return live(all, now), nil
}

func live(all []Dismissal, now time.Time) []Dismissal {
	out := make([]Dismissal, 0, len(all))
	for _, x := range all {
		if now.Before(x.Until) {
			out = append(out, x)
		}
	}
	return out
}

// Dismiss hides key, seen at level, until until, replacing an earlier
// dismissal of it. A damaged file is started again.
func (d Dismissals) Dismiss(key, level string, until, now time.Time) error {
	all, _ := d.Load(now)
	out := make([]Dismissal, 0, len(all)+1)
	for _, x := range all {
		if x.Key != key {
			out = append(out, x)
		}
	}
	return d.write(append(out, Dismissal{Key: key, Level: level, Until: until.UTC()}))
}

// Restore shows key again, or every finding when key is empty, and says
// how many dismissals it removed.
func (d Dismissals) Restore(key string, now time.Time) (int, error) {
	all, err := d.Load(now)
	if err != nil {
		return 0, d.write(nil)
	}
	out := make([]Dismissal, 0, len(all))
	for _, x := range all {
		if key != "" && x.Key != key {
			out = append(out, x)
		}
	}
	return len(all) - len(out), d.write(out)
}

func (d Dismissals) write(ds []Dismissal) error {
	if ds == nil {
		ds = []Dismissal{}
	}
	b, err := json.Marshal(ds)
	if err != nil {
		return err
	}
	return jsonfile.WriteAtomic(d.Path, b, 0o600)
}
