package teamshare

import (
	"context"
	"fmt"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// Result is one upload attempt.
type Result struct {
	Upload   teamwire.Upload
	Response teamwire.IngestResponse
	Warnings []string
}

// Sync builds the upload and sends it to the team this machine joined,
// recording the attempt in the state file. It returns teamclient.ErrNotJoined
// when there is no team to send to, which is the normal state: nothing is
// sent unless this machine joined.
func Sync(ctx context.Context, d Deps, opts Options, statePath string, now time.Time) (Result, error) {
	st, err := teamclient.Load(statePath)
	if err != nil {
		return Result{}, err
	}
	u, warnings := Build(ctx, d, opts, now)
	res := Result{Upload: u, Warnings: warnings}
	if err := u.Validate(); err != nil {
		// Never send what the server would refuse; it is a bug here.
		return res, fmt.Errorf("upload failed validation, not sent: %w", err)
	}
	resp, sendErr := teamclient.New(st.URL, st.DeviceToken).Upload(ctx, u)
	res.Response = resp
	at := now.UTC()
	st.LastUpload = &at
	if sendErr != nil {
		st.LastResult = "failed: " + sendErr.Error()
	} else {
		st.LastResult = fmt.Sprintf("sent %d rows for %d days", resp.Accepted, len(u.Days))
		if resp.Stale {
			st.LastResult += " (some days already held a newer computation)"
		}
	}
	// The state may have been removed by `tokenops team leave` while the
	// upload ran; do not resurrect it.
	if _, err := teamclient.Load(statePath); err == nil {
		_ = teamclient.Save(statePath, st)
	}
	return res, sendErr
}
