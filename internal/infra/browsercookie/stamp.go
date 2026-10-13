package browsercookie

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Stamp fingerprints the cookie stores a read would look in: each store
// and its write-ahead log or journal, by path, size and modification time.
// It opens nothing, so it costs a few stat calls where a read copies the
// whole store. A caller that found no session in the browser compares
// stamps to know when reading again could find one. only restricts it to
// one browser, as for FindMany; empty when no store exists.
func Stamp(home, only string) string {
	var b strings.Builder
	for _, br := range known {
		if only != "" && !strings.EqualFold(only, br.Name) {
			continue
		}
		stores, err := br.stores(filepath.Join(home, br.dir))
		if err != nil {
			continue
		}
		for _, store := range stores {
			for _, f := range []string{store, store + "-wal", store + "-journal"} {
				if fi, err := os.Stat(f); err == nil {
					fmt.Fprintf(&b, "%s|%d|%d\n", f, fi.Size(), fi.ModTime().UnixNano())
				}
			}
		}
	}
	return b.String()
}
