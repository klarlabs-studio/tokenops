// Package codexappserver reads Codex's plan windows by asking Codex itself:
// `codex app-server` speaks JSON-RPC over stdio, and its
// account/rateLimits/read answers with the windows Codex signs in for
// (ADR 0011, a harness feed). TokenOps never sees a credential; Codex
// authenticates its own request, as CodexBar's CLI source does.
//
// The package holds the JSON-RPC conversation over a Conn, the poller and
// the mapping to envelopes; starting the codex process and finding its
// binary live in internal/infra/vendorusage/codexappserver.
package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

// Window is one rate-limit window as Codex reports it.
type Window struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins int64   `json:"windowDurationMins"`
	ResetsAt           int64   `json:"resetsAt"`
}

// Snapshot is the part of account/rateLimits/read this source keeps: one
// limit bucket. An account has several (codex, and others such as a
// model family's), and LimitID names which one this is.
type Snapshot struct {
	// LimitID is "" or "codex" for the default bucket.
	LimitID   string  `json:"limitId"`
	LimitName string  `json:"limitName"`
	PlanType  string  `json:"planType"`
	Primary   *Window `json:"primary"`
	Secondary *Window `json:"secondary"`
	// RateLimitReachedType is set when a limit has been hit.
	RateLimitReachedType string `json:"rateLimitReachedType"`
}

// IsDefault reports whether the bucket is the default codex limit.
func (s Snapshot) IsDefault() bool {
	return s.LimitID == "" || s.LimitID == DefaultLimitID
}

// DefaultLimitID is the bucket Codex's own state names when a snapshot
// carries no limit_id (codex-rs/core/src/state/session.rs).
const DefaultLimitID = "codex"

// ErrNotSignedIn means Codex answered without a plan: an API-key login or
// none at all.
var ErrNotSignedIn = errors.New("codex-app-server: Codex reports no plan windows; it is not signed in with ChatGPT")

// rpcTimeout bounds one conversation with the app server.
const rpcTimeout = 20 * time.Second

// Conn is a running app server's stdio.
type Conn interface {
	io.Writer
	io.Reader
	Close() error
}

// Dial starts the app server. The process launcher lives in
// internal/infra/vendorusage/codexappserver; tests replace it.
type Dial func(ctx context.Context) (Conn, error)

// Read asks the app server for the account's default (codex) limit.
func Read(ctx context.Context, dial Dial) (Snapshot, error) {
	all, err := ReadAll(ctx, dial)
	if err != nil {
		return Snapshot{}, err
	}
	return all[0], nil
}

// ReadAll asks the app server for every limit bucket the account has.
// The default bucket is first; each other bucket with a window follows,
// in limit_id order. The answer is codex-rs's account/rateLimits/read:
// rateLimits is the codex bucket and rateLimitsByLimitId all of them.
func ReadAll(ctx context.Context, dial Dial) ([]Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	conn, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	enc, lines := json.NewEncoder(conn), bufio.NewScanner(conn)
	lines.Buffer(make([]byte, 64*1024), 4<<20)
	if err := enc.Encode(rpcRequest{ID: 1, Method: "initialize", Params: map[string]any{
		"clientInfo": map[string]string{"name": "tokenops", "version": "1"},
	}}); err != nil {
		return nil, err
	}
	if _, err := await(ctx, lines, 1); err != nil {
		return nil, err
	}
	if err := enc.Encode(rpcRequest{Method: "initialized"}); err != nil {
		return nil, err
	}
	if err := enc.Encode(rpcRequest{ID: 2, Method: "account/rateLimits/read", Params: map[string]any{
		"excludeResetCreditDetails": true,
	}}); err != nil {
		return nil, err
	}
	raw, err := await(ctx, lines, 2)
	if err != nil {
		return nil, err
	}
	var res struct {
		RateLimits        Snapshot            `json:"rateLimits"`
		RateLimitsByLimit map[string]Snapshot `json:"rateLimitsByLimitId"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("codex-app-server: unreadable rate limits: %w", err)
	}
	if res.RateLimits.Primary == nil && res.RateLimits.Secondary == nil {
		return nil, ErrNotSignedIn
	}
	return buckets(res.RateLimits, res.RateLimitsByLimit), nil
}

// buckets orders the account's limits: the default first, then every
// other bucket that has a window, each named by its limit_id. A bucket
// listed twice is kept once.
func buckets(def Snapshot, byID map[string]Snapshot) []Snapshot {
	out := []Snapshot{def}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	seen := map[string]bool{DefaultLimitID: true}
	if !def.IsDefault() {
		seen[def.LimitID] = true
	}
	for _, id := range ids {
		snap := byID[id]
		if snap.LimitID == "" {
			snap.LimitID = id
		}
		if seen[snap.LimitID] || snap.IsDefault() || (snap.Primary == nil && snap.Secondary == nil) {
			continue
		}
		seen[snap.LimitID] = true
		out = append(out, snap)
	}
	return out
}

type rpcRequest struct {
	ID     int    `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// await reads lines until the response to id, skipping notifications.
func await(ctx context.Context, lines *bufio.Scanner, id int) (json.RawMessage, error) {
	for lines.Scan() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var m struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(lines.Bytes(), &m) != nil || m.ID == nil || *m.ID != id {
			continue
		}
		if m.Error != nil {
			return nil, fmt.Errorf("codex-app-server: %s", m.Error.Message)
		}
		return m.Result, nil
	}
	if err := lines.Err(); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("codex-app-server: no answer in %s", rpcTimeout)
	}
	return nil, errors.New("codex-app-server: the app server closed without answering")
}
