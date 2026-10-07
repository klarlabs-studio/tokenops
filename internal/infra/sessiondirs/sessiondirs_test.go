package sessiondirs

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// isolateHome points every default root at an empty temp home, so a test
// that leaves a root unset can never read the operator's real sessions.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_DB", "")
	return home
}

func write(t *testing.T, path, body string, mtime time.Time) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func lines(ls ...string) string { return strings.Join(ls, "\n") + "\n" }

func TestClaudeDir(t *testing.T) {
	filler := make([]string, claudeHeadLines)
	for i := range filler {
		filler[i] = `{"type":"summary"}`
	}
	tests := []struct {
		name   string
		file   string
		body   string
		wantID string
		want   Dir
		wantOK bool
	}{
		{
			name:   "cwd and branch from the first line that carries context",
			file:   "abc.jsonl",
			body:   lines(`{"type":"summary"}`, `{"sessionId":"s1","cwd":"/w/repo","gitBranch":"feat/x"}`),
			wantID: "s1", want: Dir{CWD: "/w/repo", Branch: "feat/x"}, wantOK: true,
		},
		{
			name:   "session id falls back to the file name",
			file:   "from-name.jsonl",
			body:   lines(`{"cwd":"/w/repo"}`),
			wantID: "from-name", want: Dir{CWD: "/w/repo"}, wantOK: true,
		},
		{
			name:   "malformed lines are skipped",
			file:   "m.jsonl",
			body:   lines(`{not json`, `{"sessionId":"s2","cwd":"/w/two"}`),
			wantID: "s2", want: Dir{CWD: "/w/two"}, wantOK: true,
		},
		{
			name: "no line carries a cwd",
			file: "none.jsonl",
			body: lines(`{"sessionId":"s3"}`, `{"type":"user"}`),
		},
		{
			name: "empty transcript",
			file: "empty.jsonl",
		},
		{
			name: "cwd beyond the head is not looked for",
			file: "deep.jsonl",
			body: lines(append(filler, `{"sessionId":"s4","cwd":"/w/late"}`)...),
		},
		{
			name:   "cwd on the last line of the head is found",
			file:   "edge.jsonl",
			body:   lines(append(filler[:claudeHeadLines-1], `{"sessionId":"s5","cwd":"/w/edge"}`)...),
			wantID: "s5", want: Dir{CWD: "/w/edge"}, wantOK: true,
		},
	}
	dir := t.TempDir()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := write(t, filepath.Join(dir, tt.file), tt.body, time.Time{})
			id, d, ok := claudeDir(path)
			if id != tt.wantID || d != tt.want || ok != tt.wantOK {
				t.Errorf("claudeDir = %q, %+v, %v; want %q, %+v, %v", id, d, ok, tt.wantID, tt.want, tt.wantOK)
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		if _, _, ok := claudeDir(filepath.Join(dir, "absent.jsonl")); ok {
			t.Error("claudeDir on a missing file: ok = true")
		}
	})
}

func TestCodexDir(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		wantID string
		want   Dir
		wantOK bool
	}{
		{
			name:   "session_meta with git branch",
			body:   lines(`{"type":"session_meta","payload":{"id":"c1","cwd":"/w/c","git":{"branch":"main"}}}`, `{"type":"event_msg"}`),
			wantID: "c1", want: Dir{CWD: "/w/c", Branch: "main"}, wantOK: true,
		},
		{
			name:   "session_meta without git",
			body:   lines(`{"type":"session_meta","payload":{"id":"c2","cwd":"/w/d"}}`),
			wantID: "c2", want: Dir{CWD: "/w/d"}, wantOK: true,
		},
		{name: "first line is not session_meta", body: lines(`{"type":"event_msg","payload":{"id":"c3","cwd":"/w"}}`)},
		{name: "session_meta without id", body: lines(`{"type":"session_meta","payload":{"cwd":"/w"}}`)},
		{name: "session_meta without cwd", body: lines(`{"type":"session_meta","payload":{"id":"c4"}}`)},
		{name: "malformed first line", body: lines(`{"type":"session_meta"`)},
		{name: "empty rollout"},
		{
			// Only the first line is read: a later session_meta is not
			// the session's own.
			name: "session_meta after the first line",
			body: lines(`{"type":"event_msg"}`, `{"type":"session_meta","payload":{"id":"c5","cwd":"/w"}}`),
		},
	}
	dir := t.TempDir()
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := write(t, filepath.Join(dir, "rollout-"+string(rune('a'+i))+".jsonl"), tt.body, time.Time{})
			id, d, ok := codexDir(path)
			if id != tt.wantID || d != tt.want || ok != tt.wantOK {
				t.Errorf("codexDir = %q, %+v, %v; want %q, %+v, %v", id, d, ok, tt.wantID, tt.want, tt.wantOK)
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		if _, _, ok := codexDir(filepath.Join(dir, "absent.jsonl")); ok {
			t.Error("codexDir on a missing file: ok = true")
		}
	})
}

func TestChangedSince(t *testing.T) {
	dir := t.TempDir()
	since := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	old := write(t, filepath.Join(dir, "old"), "", since.Add(-time.Second))
	edge := write(t, filepath.Join(dir, "edge"), "", since)
	fresh := write(t, filepath.Join(dir, "fresh"), "", since.Add(time.Hour))
	gone := filepath.Join(dir, "gone")

	in := []string{old, edge, gone, fresh}
	got := changedSince(in, since)
	if strings.Join(got, ",") != strings.Join([]string{edge, fresh}, ",") {
		t.Errorf("changedSince = %v, want the file at the boundary and the fresh one, in order", got)
	}
	if in[0] != old || in[1] != edge || in[2] != gone || in[3] != fresh {
		t.Errorf("changedSince reordered its input: %v", in)
	}
	if got := changedSince(nil, since); len(got) != 0 {
		t.Errorf("changedSince(nil) = %v", got)
	}
}

func TestOrDefault(t *testing.T) {
	ok := func() (string, error) { return "/default", nil }
	fail := func() (string, error) { return "", errors.New("no home") }
	tests := []struct {
		name, root string
		def        func() (string, error)
		want       string
	}{
		{"explicit root wins", "/given", fail, "/given"},
		{"empty root reads the default", "", ok, "/default"},
		{"unresolvable default is skipped", "", fail, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := orDefault(tt.root, tt.def); got != tt.want {
				t.Errorf("orDefault = %q, want %q", got, tt.want)
			}
		})
	}
}

func newOpencodeDB(t *testing.T, path string, rows map[string]string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for id, dir := range rows {
		if _, err := db.Exec(`INSERT INTO session (id, directory) VALUES (?, ?)`, id, dir); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFindMergesEveryClient(t *testing.T) {
	isolateHome(t)
	base := t.TempDir()
	since := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	fresh := since.Add(time.Hour)
	roots := Roots{
		Claude:   filepath.Join(base, "claude"),
		Codex:    filepath.Join(base, "codex"),
		Opencode: filepath.Join(base, "opencode.db"),
	}
	write(t, filepath.Join(roots.Claude, "proj", "cc-new.jsonl"),
		lines(`{"sessionId":"cc-new","cwd":"/w/claude","gitBranch":"main"}`), fresh)
	write(t, filepath.Join(roots.Claude, "proj", "cc-old.jsonl"),
		lines(`{"sessionId":"cc-old","cwd":"/w/stale"}`), since.Add(-time.Hour))
	write(t, filepath.Join(roots.Claude, "proj", "cc-nocwd.jsonl"),
		lines(`{"sessionId":"cc-nocwd"}`), fresh)
	write(t, filepath.Join(roots.Codex, "2026", "09", "10", "rollout-1.jsonl"),
		lines(`{"type":"session_meta","payload":{"id":"cx-1","cwd":"/w/codex","git":{"branch":"dev"}}}`), fresh)
	write(t, filepath.Join(roots.Codex, "2026", "09", "10", "notes.jsonl"),
		lines(`{"type":"session_meta","payload":{"id":"cx-ignored","cwd":"/w/x"}}`), fresh)
	newOpencodeDB(t, roots.Opencode, map[string]string{"oc-1": "/w/opencode", "oc-blank": ""})

	got := Find(roots, since)
	want := map[string]Dir{
		"cc-new": {CWD: "/w/claude", Branch: "main"},
		"cx-1":   {CWD: "/w/codex", Branch: "dev"},
		"oc-1":   {CWD: "/w/opencode"},
	}
	if len(got) != len(want) {
		t.Errorf("Find = %+v, want %+v", got, want)
	}
	for id, d := range want {
		if got[id] != d {
			t.Errorf("Find[%q] = %+v, want %+v", id, got[id], d)
		}
	}
}

// A client whose records cannot be read is left out; the others still
// answer.
func TestFindSkipsUnreadableClients(t *testing.T) {
	isolateHome(t)
	base := t.TempDir()
	since := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	notADB := write(t, filepath.Join(base, "opencode.db"), "this is not sqlite", time.Time{})
	roots := Roots{
		Claude:   filepath.Join(base, "missing-claude"),
		Codex:    filepath.Join(base, "codex"),
		Opencode: notADB,
	}
	write(t, filepath.Join(roots.Codex, "rollout-1.jsonl"),
		lines(`{"type":"session_meta","payload":{"id":"cx-1","cwd":"/w/codex"}}`), since.Add(time.Minute))

	got := Find(roots, since)
	if len(got) != 1 || got["cx-1"].CWD != "/w/codex" {
		t.Errorf("Find = %+v, want only the codex session", got)
	}
}

// Unset roots read each client's default under HOME — never anywhere
// else — and an operator with no clients installed gets an empty map.
func TestFindDefaultsResolveUnderHome(t *testing.T) {
	home := isolateHome(t)
	since := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	if got := Find(Roots{}, since); len(got) != 0 {
		t.Fatalf("Find with an empty home = %+v, want empty", got)
	}

	write(t, filepath.Join(home, ".claude", "projects", "proj", "s.jsonl"),
		lines(`{"sessionId":"home-cc","cwd":"/w/home"}`), since.Add(time.Hour))
	write(t, filepath.Join(home, ".codex", "sessions", "rollout-h.jsonl"),
		lines(`{"type":"session_meta","payload":{"id":"home-cx","cwd":"/w/hcx"}}`), since.Add(time.Hour))
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "opencode"), 0o750); err != nil {
		t.Fatal(err)
	}
	newOpencodeDB(t, filepath.Join(home, ".local", "share", "opencode", "opencode.db"), map[string]string{"home-oc": "/w/hoc"})

	got := Find(Roots{}, since)
	for id, cwd := range map[string]string{"home-cc": "/w/home", "home-cx": "/w/hcx", "home-oc": "/w/hoc"} {
		if got[id].CWD != cwd {
			t.Errorf("Find[%q] = %+v, want cwd %q (all %+v)", id, got[id], cwd, got)
		}
	}
}
