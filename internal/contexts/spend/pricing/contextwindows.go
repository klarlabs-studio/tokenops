package pricing

import (
	"os"
	"strconv"
	"sync"
	"time"

	"go.klarlabs.de/tokenops/internal/contexts/spend/spend"
)

// windowCache holds the context windows read from one snapshot directory,
// valid for as long as that directory's stamp is unchanged.
var windowCache struct {
	mu      sync.Mutex
	loaded  bool
	dir     string
	stamp   string
	windows map[string]int64
}

func init() {
	// Every process that links the pricing catalog answers the Claude table
	// first and the catalog second, from the default snapshot directory.
	spend.SetCatalogWindows(func() map[string]int64 { return ContextWindows("") })
}

// ContextWindows returns the input windows the snapshots under dir state,
// keyed by bare model name, the newest snapshot's answer winning. Snapshots
// are read only when the snapshots directory's stamp changes. SaveSnapshot
// writes a temporary file and renames it into place, which changes the
// directory's modification time, so a refresh is picked up by the next
// lookup, which costs one stat. The map is shared: do not modify it.
func ContextWindows(dir string) map[string]int64 {
	root := ResolveDir(dir)
	stamp := snapshotsStamp(dir)

	windowCache.mu.Lock()
	defer windowCache.mu.Unlock()
	if windowCache.loaded && windowCache.dir == root && windowCache.stamp == stamp {
		return windowCache.windows
	}
	out := map[string]int64{}
	// LoadSnapshots sorts ascending by FetchedAt, so a later snapshot's
	// window replaces an earlier one's.
	for _, s := range LoadSnapshots(dir) {
		for name, w := range s.ContextWindows {
			out[name] = w
		}
	}
	windowCache.loaded, windowCache.dir, windowCache.stamp, windowCache.windows = true, root, stamp, out
	return out
}

// snapshotsStamp is the snapshots directory's modification time and size,
// empty when it is missing. Creating or renaming a file in it changes both
// on every platform that matters here.
func snapshotsStamp(dir string) string {
	fi, err := os.Stat(snapshotsDir(dir))
	if err != nil {
		return ""
	}
	return fi.ModTime().UTC().Format(time.RFC3339Nano) + "/" + strconv.FormatInt(fi.Size(), 10)
}
