// Package sessiondirs maps agent sessions to the directory they ran in,
// from the clients' own records: the first lines of Claude Code's and
// Codex's transcripts, and opencode's session table. Attributing cost to
// a repository, and from there to a commit, starts here.
package sessiondirs

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudecodejsonl"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexjsonl"
	"go.klarlabs.de/tokenops/internal/contexts/telemetry/opencodedb"
)

// Dir is where a session ran.
type Dir struct {
	CWD string
	// Branch is the git branch the client recorded, when it did.
	Branch string
}

// Roots are where each client keeps its sessions; zero values read the
// defaults.
type Roots struct {
	Claude, Codex, Opencode string
}

// claudeHeadLines is how far into a Claude Code transcript the working
// directory is looked for: it appears on the first line that carries
// context, a handful of lines in.
const claudeHeadLines = 200

// Find maps every session changed since since to its directory. A client
// whose records cannot be read is left out.
func Find(roots Roots, since time.Time) map[string]Dir {
	out := map[string]Dir{}
	if root := orDefault(roots.Claude, claudecodejsonl.DefaultRoot); root != "" {
		if files, err := claudecodejsonl.FindSessionFiles(root); err == nil {
			for _, f := range changedSince(files, since) {
				if id, d, ok := claudeDir(f); ok {
					out[id] = d
				}
			}
		}
	}
	if root := orDefault(roots.Codex, codexjsonl.DefaultRoot); root != "" {
		if files, err := codexjsonl.FindSessionFiles(root); err == nil {
			for _, f := range changedSince(files, since) {
				if id, d, ok := codexDir(f); ok {
					out[id] = d
				}
			}
		}
	}
	if path := orDefault(roots.Opencode, opencodedb.DefaultPath); path != "" {
		if dirs, err := opencodedb.SessionDirs(path, since); err == nil {
			for id, cwd := range dirs {
				out[id] = Dir{CWD: cwd}
			}
		}
	}
	return out
}

func orDefault(root string, def func() (string, error)) string {
	if root != "" {
		return root
	}
	r, err := def()
	if err != nil {
		return ""
	}
	return r
}

func changedSince(files []string, since time.Time) []string {
	out := files[:0:0]
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && !st.ModTime().Before(since) {
			out = append(out, f)
		}
	}
	return out
}

// claudeDir reads a Claude Code transcript's first lines for its session
// and working directory.
func claudeDir(path string) (string, Dir, bool) {
	f, err := os.Open(path) //nolint:gosec // a transcript under Claude Code's projects directory
	if err != nil {
		return "", Dir{}, false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 16<<20)
	for i := 0; i < claudeHeadLines && sc.Scan(); i++ {
		var line struct {
			SessionID string `json:"sessionId"`
			CWD       string `json:"cwd"`
			GitBranch string `json:"gitBranch"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.CWD == "" {
			continue
		}
		id := line.SessionID
		if id == "" {
			id = strings.TrimSuffix(filepath.Base(path), ".jsonl")
		}
		return id, Dir{CWD: line.CWD, Branch: line.GitBranch}, true
	}
	return "", Dir{}, false
}

// codexDir reads a Codex rollout's session_meta line.
func codexDir(path string) (string, Dir, bool) {
	f, err := os.Open(path) //nolint:gosec // a rollout under Codex's sessions directory
	if err != nil {
		return "", Dir{}, false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<16), 16<<20)
	if !sc.Scan() {
		return "", Dir{}, false
	}
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			ID  string `json:"id"`
			CWD string `json:"cwd"`
			Git struct {
				Branch string `json:"branch"`
			} `json:"git"`
		} `json:"payload"`
	}
	if json.Unmarshal(sc.Bytes(), &meta) != nil || meta.Type != "session_meta" || meta.Payload.ID == "" || meta.Payload.CWD == "" {
		return "", Dir{}, false
	}
	return meta.Payload.ID, Dir{CWD: meta.Payload.CWD, Branch: meta.Payload.Git.Branch}, true
}
