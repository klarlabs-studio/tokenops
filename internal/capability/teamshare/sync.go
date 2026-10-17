package teamshare

import (
	"context"
	"errors"
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
	if opts.Allow != nil {
		if err := opts.Allow(); err != nil {
			return Result{}, err
		}
	}
	client := teamclient.New(st.URL, st.DeviceToken)
	opts.Schema = ServerSchema(ctx, client, &st, now)
	u, warnings := Build(ctx, d, opts, now)
	res := Result{Upload: u, Warnings: warnings}
	if err := u.Validate(); err != nil {
		// Never send what the server would refuse; it is a bug here.
		return res, fmt.Errorf("upload failed validation, not sent: %w", err)
	}
	resp, sendErr := client.Upload(ctx, u)
	res.Response = resp
	at := now.UTC()
	st.LastUpload = &at
	var se *teamclient.ServerError
	if u.Schema >= teamwire.SchemaFinOps && errors.As(sendErr, &se) && se.Status >= 400 && se.Status < 500 {
		// A server that said it reads schema 2 refused one: ask it again
		// before the next upload rather than trust the old answer.
		st.SchemaCheckedAt = nil
	}
	switch {
	case teamclient.IsCode(sendErr, teamwire.CodeUploadsPaused):
		paused := &PausedError{Days: opts.Days}
		errors.As(sendErr, &paused.Server)
		sendErr = paused
		st.LastResult = "paused: the organisation has no active trial or subscription"
	case sendErr != nil:
		st.LastResult = "failed: " + sendErr.Error()
	default:
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

// SchemaRecheck is how long the server's max_schema is trusted before it
// is asked again: a server upgrade reaches the clients within a day.
const SchemaRecheck = 24 * time.Hour

// MeReader asks the server about this member; *teamclient.Client is one.
type MeReader interface {
	Me(ctx context.Context) (teamwire.Me, error)
}

// ServerSchema is the newest upload format the server accepts, from
// st's cached answer while it is fresh, else asked again (and noted in
// st, which the caller saves). A server that cannot be asked keeps the
// cached answer; with none, it is schema 1, which every server reads.
func ServerSchema(ctx context.Context, c MeReader, st *teamclient.State, now time.Time) int {
	fresh := st.SchemaCheckedAt != nil && now.Sub(*st.SchemaCheckedAt) < SchemaRecheck && !now.Before(*st.SchemaCheckedAt)
	if !fresh && c != nil {
		if me, err := c.Me(ctx); err == nil {
			st.NoteSchema(me.MaxSchema, now)
		}
	}
	if st.MaxSchema < teamwire.SchemaBuckets {
		return teamwire.SchemaBuckets
	}
	return min(st.MaxSchema, teamwire.SchemaVersion)
}

// PausedError is an upload the server refused because the organisation has
// no active trial or subscription. Nothing is lost here: each upload
// resends the last Days days whole, so the first one after a subscription
// starts fills the gap up to that many days.
type PausedError struct {
	Server *teamclient.ServerError
	Days   int
}

func (e *PausedError) Error() string {
	msg := "team uploads are paused: the organisation's trial ended or its subscription lapsed"
	if e.Server != nil && e.Server.Message != "" {
		msg = "team uploads are paused: " + e.Server.Message
	}
	msg += ". The figures already sent stay viewable, read-only, until they are deleted"
	if e.Server != nil && e.Server.Hint != "" {
		msg += " (" + e.Server.Hint + ")"
	}
	msg += ". An owner or admin can subscribe in the web view or with `tokenops team admin billing --checkout`"
	if e.Days > 0 {
		msg += fmt.Sprintf("; the next upload after that resends the last %d days", e.Days)
	}
	return msg + "."
}
