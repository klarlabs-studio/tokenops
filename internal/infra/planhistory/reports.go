package planhistory

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Report is the plan a client last reported for a provider, and when it
// first reported it. FirstSeen moves only when the reported plan changes,
// so it answers "since when does the client say this?" — the moment a
// plan TokenOps follows takes effect from, and the line an operator's own
// later choice is weighed against.
type Report struct {
	Plan      string    `json:"plan"`
	Client    string    `json:"client"`
	Detail    string    `json:"detail,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	// FollowedFrom and FollowedAt record the last time TokenOps rebound
	// the provider to this plan on the client's word, and from what.
	FollowedFrom string    `json:"followed_from,omitempty"`
	FollowedAt   time.Time `json:"followed_at,omitzero"`
}

// Reports is the JSON file of each provider's last client report.
type Reports struct {
	Path string
}

// DefaultReports is the file under the operator's home directory.
func DefaultReports() (Reports, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Reports{}, err
	}
	return Reports{Path: filepath.Join(home, ".tokenops", "plan-reports.json")}, nil
}

// Load reads the reports. A missing or unreadable file is no reports:
// the next one observed starts afresh.
func (r Reports) Load() (map[string]Report, error) {
	out := map[string]Report{}
	data, err := os.ReadFile(r.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if json.Unmarshal(data, &out) != nil {
		return map[string]Report{}, nil
	}
	return out, nil
}

// Save replaces the file atomically.
func (r Reports) Save(reports map[string]Report) error {
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.Path), ".plan-reports-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), r.Path)
}
