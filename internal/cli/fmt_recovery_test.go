package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The recovery store holds the full raw stdout and stderr of whatever
// command was wrapped. That is the one place in TokenOps that writes
// unfiltered command output to disk, so it is also the one place a
// credential printed by a sub-command comes to rest.
//
// Nothing else on this machine should be able to read it.
func TestRecoveryFilesAreNotWorldReadable(t *testing.T) {
	dir := t.TempDir()
	// A sub-directory that does not exist yet, so the test covers the
	// permissions writeRecovery creates it with rather than the ones
	// t.TempDir happens to use.
	store := filepath.Join(dir, "recovery")

	path, err := writeRecovery(store, []string{"echo", "hi"}, []byte("out"), []byte("err"), 0)
	if err != nil {
		t.Fatalf("writeRecovery: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("recovery file mode = %#o, want 0600 (it holds raw command output)", perm)
	}

	di, err := os.Stat(store)
	if err != nil {
		t.Fatalf("stat %s: %v", store, err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("recovery dir mode = %#o, want 0700", perm)
	}
}

// A command that prints a key writes that key into the recovery file.
// `redaction` already knows how to find one; it was wired only into the
// OTLP exporter, which is the path least likely to carry a secret.
func TestRecoveryRedactsSecretsItCaptures(t *testing.T) {
	const key = "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	dir := t.TempDir()

	path, err := writeRecovery(dir, []string{"printenv"},
		[]byte("ANTHROPIC_API_KEY="+key+"\n"), nil, 0)
	if err != nil {
		t.Fatalf("writeRecovery: %v", err)
	}
	body, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(body), key) {
		t.Error("the recovery file stored an API key verbatim")
	}
	// The line has to survive in a recognisable form — recovery exists so
	// an operator can read what the command actually said.
	if !strings.Contains(string(body), "ANTHROPIC_API_KEY=") {
		t.Errorf("redaction removed the surrounding output:\n%s", body)
	}
}

// The argv is written into the recovery header, and a key passed as a
// flag lives there rather than in the output.
func TestRecoveryRedactsTheCommandLine(t *testing.T) {
	const key = "sk-ant-api03-BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	dir := t.TempDir()

	path, err := writeRecovery(dir, []string{"curl", "-H", "authorization: Bearer " + key}, []byte("ok"), nil, 0)
	if err != nil {
		t.Fatalf("writeRecovery: %v", err)
	}
	body, err := os.ReadFile(path) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(body), key) {
		t.Error("the recovery header stored a key from the command line")
	}
}

// Nothing pruned this directory. A machine running `tokenops fmt` under a
// shell hook accumulates one file per command, forever, each holding full
// command output.
func TestRecoveryPrunesOldFiles(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-(recoveryMaxAge + time.Hour))

	stale := filepath.Join(dir, "20200101T000000-aaaaaaaaaaaa.out")
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	// Something that is not ours, aged the same way: pruning must not
	// treat the directory as its own.
	foreign := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Chtimes(foreign, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	fresh, err := writeRecovery(dir, []string{"echo", "hi"}, []byte("out"), nil, 0)
	if err != nil {
		t.Fatalf("writeRecovery: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a recovery file older than %s survived: %v", recoveryMaxAge, err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("pruning deleted a file it did not write: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("pruning deleted the file just written: %v", err)
	}
}

// A failed prune must not lose the output. Recovery exists so detail is
// never lost; deleting is the side errand.
func TestRecoveryStillWritesWhenPruneCannotRead(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "store")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Directory unreadable but still writable: ReadDir fails, create
	// succeeds.
	if err := os.Chmod(sub, 0o300); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })

	if _, err := writeRecovery(sub, []string{"echo", "hi"}, []byte("out"), nil, 0); err != nil {
		t.Errorf("a prune failure lost the output: %v", err)
	}
}

// The end-to-end path, not just the writer: what runFmt puts on disk is
// what a real `tokenops fmt` leaves behind.
func TestRunFmtRecoveryIsPrivate(t *testing.T) {
	dir := t.TempDir()
	res, err := runFmt(context.Background(), testRegistry(),
		[]string{"echo", "hello"}, fmtOptions{RecoverDir: filepath.Join(dir, "store")})
	if err != nil {
		t.Fatalf("runFmt: %v", err)
	}
	if res.RecoveryPath == "" {
		t.Fatal("no recovery file was written")
	}
	fi, err := os.Stat(res.RecoveryPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("recovery file mode = %#o, want 0600", perm)
	}
}
