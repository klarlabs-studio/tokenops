package sourceprobe

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/codexrollout"
)

// fullRewalkEvery is how often a probe walks its whole tree again however
// unchanged its directories look. The warm probe is exact on the file
// systems the daemon runs on (see treeCache); this bounds how long an
// answer can stay wrong on one that is not, such as a network mount whose
// directory times do not move. Ten minutes is far inside the staleness
// window a probe feeds, and costs one full walk in twenty at the 30 s
// refresh.
const fullRewalkEvery = 10 * time.Minute

// settleWindow is how close to the probe a directory's change stamp may be
// before its listing is not trusted on the next probe. A file system with
// coarse timestamps (FAT keeps two seconds) can take a new entry in the
// same tick the directory was listed in, leaving the stamp unchanged.
const settleWindow = 2 * time.Second

// transcripts remembers the transcript trees the daemon probes every
// refresh. One process-wide cache, keyed by root, because the probes are
// built afresh from the config on every assessment.
var transcripts = newTreeCache(fullRewalkEvery, time.Now)

// isTranscript is what the JSONL readers ingest: a transcript, or a cold
// Codex rollout, which is only ever its compressed copy.
func isTranscript(path string) bool {
	return strings.HasSuffix(path, ".jsonl") || codexrollout.IsCompressed(path)
}

// treeCache answers "the newest matching file under root" without reading
// every directory on every probe.
//
// Adding, removing or renaming an entry changes its directory's
// modification and change times, and only that directory's: not its
// parent's. Appending to a file changes the file's times and not its
// directory's. (Checked on APFS; ext4 behaves the same.) So a warm probe
// stats every directory it knows, lists again only those whose identity
// or stamp moved or that were too fresh to trust, and stats every
// matching file it knows, which is what catches an append. It skips the
// listing of every unchanged directory, which is most of a full walk's
// cost.
//
// The change time is compared as well as the modification time because
// the modification time can be set back by hand, and the change time
// cannot; the identity, because a directory removed and recreated under
// the same name is a different directory.
type treeCache struct {
	mu        sync.Mutex
	roots     map[string]*treeState
	fullEvery time.Duration
	now       func() time.Time
	// listings counts directory reads, for the tests.
	listings int
}

type treeState struct {
	walkedAt time.Time
	dirs     map[string]*dirListing
}

// dirListing is what one directory held when it was last read.
type dirListing struct {
	info    fs.FileInfo
	stamp   dirStamp
	settled bool
	subdirs []string
	files   []string
}

// dirStamp is what moves when a directory's entries change.
type dirStamp struct {
	mod, change time.Time
}

func stampOf(info fs.FileInfo) dirStamp {
	return dirStamp{mod: info.ModTime(), change: changeTime(info)}
}

func newTreeCache(fullEvery time.Duration, now func() time.Time) *treeCache {
	return &treeCache{roots: map[string]*treeState{}, fullEvery: fullEvery, now: now}
}

// newest reports the modification time of the newest file under root that
// match accepts, exactly as walkNewest would.
func (c *treeCache) newest(root string, match func(string) bool) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		// A root that is a symlink, or not there: the full walk's answer
		// is cheap and is the reference.
		delete(c.roots, root)
		return walkNewest(root, match)
	}
	now := c.now()
	prev := c.roots[root]
	if prev != nil && now.Sub(prev.walkedAt) >= c.fullEvery {
		prev = nil
	}
	next := &treeState{walkedAt: now, dirs: map[string]*dirListing{}}
	if prev != nil {
		next.walkedAt = prev.walkedAt
	}
	var newest time.Time
	c.visit(root, info, prev, next, match, now, &newest)
	c.roots[root] = next
	return newest.UTC(), true
}

// visit takes in dir, whose own Lstat is info, and everything under it.
func (c *treeCache) visit(dir string, info fs.FileInfo, prev, next *treeState, match func(string) bool, now time.Time, newest *time.Time) {
	listing := c.listing(dir, info, prev, match, now)
	next.dirs[dir] = listing
	for _, f := range listing.files {
		fi, err := os.Lstat(f)
		if err != nil {
			continue // removed since the listing: its directory says so next time
		}
		if fi.ModTime().After(*newest) {
			*newest = fi.ModTime()
		}
	}
	for _, sub := range listing.subdirs {
		si, err := os.Lstat(sub)
		if err != nil || !si.IsDir() {
			continue
		}
		c.visit(sub, si, prev, next, match, now, newest)
	}
}

// listing is dir's entries: the remembered ones when dir is the same
// directory with the same stamp and was settled when read, else a fresh
// read.
func (c *treeCache) listing(dir string, info fs.FileInfo, prev *treeState, match func(string) bool, now time.Time) *dirListing {
	stamp := stampOf(info)
	if prev != nil {
		if old, ok := prev.dirs[dir]; ok && old.settled && old.stamp == stamp && os.SameFile(old.info, info) {
			return old
		}
	}
	c.listings++
	l := &dirListing{info: info, stamp: stamp, settled: settled(stamp, now)}
	// Entries read before an error still count, as they do in a full
	// walk; the listing is read again next time.
	entries, err := os.ReadDir(dir)
	if err != nil {
		l.settled = false
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			l.subdirs = append(l.subdirs, p)
		case match(p):
			l.files = append(l.files, p)
		}
	}
	return l
}

// settled reports whether a directory stamped at s is old enough, at now,
// that an entry added since would have moved the stamp.
func settled(s dirStamp, now time.Time) bool {
	latest := s.mod
	if s.change.After(latest) {
		latest = s.change
	}
	return now.Sub(latest) >= settleWindow
}

// walkNewest is the full walk: the modification time of the newest file
// under root that match accepts. It is the reference the cache must agree
// with.
func walkNewest(root string, match func(string) bool) (time.Time, bool) {
	var newest time.Time
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip it rather than fail the probe
		}
		if d.IsDir() || !match(path) {
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
		return nil
	})
	if walkErr != nil {
		return time.Time{}, false
	}
	return newest.UTC(), true
}
