package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// errNoDaemon is a daemon that is not running, or never ran here.
var errNoDaemon = errors.New("the TokenOps daemon is not running")

// errSlow is a daemon that is running but did not answer in time.
var errSlow = errors.New("the TokenOps daemon did not answer in time")

// daemon is the local TokenOps daemon's API (ADR 0010). The app reads it
// with the token the daemon leaves in ~/.tokenops/daemon.url; the token
// stays in this process and never reaches the panel.
type daemon struct {
	// urlFile is where the daemon publishes its address and token.
	urlFile string
	hc      *http.Client
}

func newDaemon() *daemon {
	home, _ := os.UserHomeDir()
	return &daemon{
		urlFile: filepath.Join(home, ".tokenops", "daemon.url"),
		// A daemon busy ingesting can take seconds to answer; the menu bar
		// keeps its last reading meanwhile.
		hc: &http.Client{Timeout: 45 * time.Second},
	}
}

// endpoint is what the daemon publishes about itself.
type endpoint struct {
	URL   string `json:"url"`
	Token string `json:"dashboard_token"`
}

func (d *daemon) endpoint() (endpoint, error) {
	b, err := os.ReadFile(d.urlFile)
	if errors.Is(err, os.ErrNotExist) {
		return endpoint{}, errNoDaemon
	}
	if err != nil {
		return endpoint{}, err
	}
	var e endpoint
	if err := json.Unmarshal(b, &e); err != nil || e.URL == "" {
		return endpoint{}, fmt.Errorf("unreadable %s", d.urlFile)
	}
	return e, nil
}

// do calls the API and decodes the answer into out.
func (d *daemon) do(ctx context.Context, method, path string, body, out any) error {
	e, err := d.endpoint()
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, e.URL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+e.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.hc.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return errSlow
		}
		return errNoDaemon
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("%s %s: status %d", method, path, resp.StatusCode)
	}
	return json.Unmarshal(b, out)
}

// glance is the one-call view: session budgets, plan headroom, insight.
func (d *daemon) glance(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	err := d.do(ctx, http.MethodGet, "/api/glance", nil, &out)
	return out, err
}

// coach is the coach's dials and preset.
func (d *daemon) coach(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	err := d.do(ctx, http.MethodGet, "/api/coach", nil, &out)
	return out, err
}

// setPreset applies a coach preset through the API, which audits it.
func (d *daemon) setPreset(ctx context.Context, preset string) (json.RawMessage, error) {
	var out json.RawMessage
	err := d.do(ctx, http.MethodPost, "/api/coach", map[string]string{"preset": preset}, &out)
	return out, err
}
