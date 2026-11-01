package teamwire

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// revertsUpload is a valid schema 4 upload: the schema 3 one with a
// reverted commit on its unit row.
func revertsUpload() Upload {
	u := spendUpload()
	u.Schema = SchemaReverts
	u.Units[0].RevertedCommits, u.Units[0].RevertedCommitUSD = 1, 0.4
	return u
}

func TestValidateReverts(t *testing.T) {
	if err := revertsUpload().Validate(); err != nil {
		t.Fatalf("valid schema 4 upload rejected: %v", err)
	}
	// A reverted commit with no work placed on it is still a reverted
	// commit.
	u := revertsUpload()
	u.Units[0].RevertedCommitUSD = 0
	if err := u.Validate(); err != nil {
		t.Errorf("reverted commit without placed work rejected: %v", err)
	}
	cases := map[string]func(u *Upload){
		"reverts in schema 3":      func(u *Upload) { u.Schema = SchemaSpend },
		"more reverted than made":  func(u *Upload) { u.Units[0].RevertedCommits = 4 },
		"negative reverted":        func(u *Upload) { u.Units[0].RevertedCommits = -1 },
		"value on no revert":       func(u *Upload) { u.Units[0].RevertedCommits = 0 },
		"reverted value too large": func(u *Upload) { u.Units[0].RevertedCommitUSD = 1.5 },
		"negative reverted value":  func(u *Upload) { u.Units[0].RevertedCommitUSD = -0.1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			u := revertsUpload()
			mutate(&u)
			if err := u.Validate(); err == nil {
				t.Errorf("accepted %+v", u.Units)
			}
		})
	}
}

// unitBucketV3 is a unit row exactly as schema 3 servers know it.
type unitBucketV3 struct {
	Day         string  `json:"day"`
	Repo        string  `json:"repo"`
	Tasks       int64   `json:"tasks"`
	TaskUSD     float64 `json:"task_usd"`
	Commits     int64   `json:"commits"`
	CommitUSD   float64 `json:"commit_usd"`
	MergedPRs   int64   `json:"merged_prs"`
	MergedPRUSD float64 `json:"merged_pr_usd"`
}

// A schema 3 server keeps working: the reverted commits are dropped, and
// the sender's own rows keep theirs.
func TestSchema3UploadDecodesOnASchema3Server(t *testing.T) {
	full := revertsUpload()
	sent := full.ForServer(SchemaSpend)
	if sent.Schema != SchemaSpend {
		t.Fatalf("schema = %d", sent.Schema)
	}
	if err := sent.Validate(); err != nil {
		t.Fatalf("schema 3 upload invalid: %v", err)
	}
	raw, err := json.Marshal(sent.Units)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var old []unitBucketV3
	if err := dec.Decode(&old); err != nil {
		t.Fatalf("a schema 3 server refuses the units: %v\n%s", err, raw)
	}
	if strings.Contains(string(raw), "reverted") {
		t.Errorf("schema 3 units carry reverts: %s", raw)
	}
	if full.Units[0].RevertedCommits != 1 {
		t.Error("ForServer changed the sender's rows")
	}
	if u := revertsUpload().ForServer(SchemaReverts); u.Units[0].RevertedCommits != 1 || u.Schema != SchemaReverts {
		t.Errorf("a schema 4 server lost the reverts: %+v", u.Units[0])
	}
}

func TestRevertsJSONNames(t *testing.T) {
	raw, err := json.Marshal(revertsUpload().Units[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"reverted_commits":1`, `"reverted_commit_usd":0.4`} {
		if !strings.Contains(string(raw), k) {
			t.Errorf("unit JSON lacks %s: %s", k, raw)
		}
	}
}
