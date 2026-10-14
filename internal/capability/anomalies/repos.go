package anomalies

import (
	"context"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/gitlog"
	"go.klarlabs.de/tokenops/internal/infra/sessiondirs"
)

// SessionRepos names the repository each session ran in, by its root
// directory's name, from where the clients recorded each session ran. A
// session outside a repository is left out.
func SessionRepos(repos *gitlog.Repos) func(ctx context.Context, since time.Time) map[string]string {
	if repos == nil {
		repos = gitlog.NewRepos()
	}
	return func(ctx context.Context, since time.Time) map[string]string {
		out := map[string]string{}
		for id, dir := range sessiondirs.Find(sessiondirs.Roots{}, since) {
			if root := repos.Root(ctx, dir.CWD); root != "" {
				out[id] = filepath.Base(root)
			}
		}
		return out
	}
}
