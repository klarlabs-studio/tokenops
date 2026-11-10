package teamshare

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/infra/teamclient"
	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// Result is one upload attempt.
type Result struct {
	Upload   teamwire.Upload
	Response teamwire.IngestResponse
	// Warnings name the rows left out of an upload that was sent.
	Warnings []string
}

// Sync builds the upload and sends it to the team this machine joined,
// recording the attempt in the state file. It returns teamclient.ErrNotJoined
// when there is no team to send to, which is the normal state: nothing is
// sent unless this machine joined.
//
// An upload is sent whole or not at all. The server replaces each day an
// upload covers, so one built while a reader failed would put days with
// figures missing over days that have them, and report success. Sync
// returns an *IncompleteError instead and the server keeps what it has;
// the next upload covers the same days.
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
	opts.Role = st.Role
	u, notes := Build(ctx, d, opts, now)
	res := Result{Upload: u, Warnings: notes.Dropped}
	if len(notes.Unread) > 0 {
		incomplete := &IncompleteError{Unread: notes.Unread}
		at := now.UTC()
		st.LastUpload, st.LastResult = &at, "not sent: "+incomplete.reason()
		saveIfJoined(statePath, st)
		return res, incomplete
	}
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
		if opts.FullDays > 0 {
			paused.Days = opts.FullDays
		}
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
		if resp.VendorCostIgnored {
			// The member is no longer an owner or admin: ask again
			// before the next upload.
			st.LastResult += " (vendor cost dropped: only an owner's or admin's machine may send it)"
			st.SchemaCheckedAt = nil
		}
	}
	saveIfJoined(statePath, st)
	return res, sendErr
}

// saveIfJoined records the attempt. The state may have been removed by
// `tokenops team leave` while the upload ran; it is not resurrected.
func saveIfJoined(statePath string, st teamclient.State) {
	if _, err := teamclient.Load(statePath); err == nil {
		_ = teamclient.Save(statePath, st)
	}
}

// IncompleteError is an upload that was built and not sent, because part
// of its figures could not be read. Nothing is lost: the server keeps the
// days as they were, and the next upload reads them again.
type IncompleteError struct {
	// Unread names each part that could not be read, with the failure.
	Unread []string
}

func (e *IncompleteError) reason() string {
	return "could not read " + strings.Join(e.Unread, "; ")
}

func (e *IncompleteError) Error() string {
	return "team upload not sent: " + e.reason() +
		". An upload replaces every day it covers, so the team keeps the figures it has until all of it can be read"
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
			st.Role = me.Role
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
