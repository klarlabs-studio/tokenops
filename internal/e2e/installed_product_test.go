// Package e2e drives the installed product: it builds the tokenops
// binary and runs it as a subprocess through the journey an operator
// actually performs — init, start, ingest, reconstruct, query, stop.
//
// Every other test in this repo exercises packages in process, where the
// wiring between those steps is supplied by the test rather than by the
// binary: internal/daemon/e2e_test.go calls daemon.Run with a config
// literal it wrote itself, so it cannot see whether `tokenops init`
// produces a config that `tokenops start` can ingest through. That gap is
// where a 27-day silent ingestion outage lived, and it is what ADR 0004
// phase 7 asks for.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// journeyBudget bounds the whole run. A daemon that never becomes
	// ready, or a poller that never publishes, has to fail the test
	// rather than hold a CI worker until the package timeout: a hang
	// reported as a hang is a worse bug report than a hang reported as
	// "ingestion produced no events within 3m".
	journeyBudget = 3 * time.Minute
	// pollInterval paces every wait loop below.
	pollInterval = 250 * time.Millisecond
	// shutdownBudget is how long the daemon gets to exit after SIGTERM.
	// Generous against a loaded machine, finite so a stuck shutdown is a
	// failure and not a hang.
	shutdownBudget = 45 * time.Second
)

// TestInstalledBinaryIngestsATranscriptAndServesItBack is the
// installed-product journey. It builds the binary, configures a
// throwaway installation under a synthetic HOME, feeds one Claude Code
// transcript turn in before boot and one after readiness, and requires
// both to come back out of the CLI and the HTTP API before the daemon
// shuts down on a signal.
//
// The two turns are not redundant. The first proves the poller's startup
// scan reaches the store; the second proves it keeps tailing after the
// daemon has declared itself ready — the half that failed silently for
// 27 days, during which every surface reported a healthy daemon.
func TestInstalledBinaryIngestsATranscriptAndServesItBack(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: this test compiles the tokenops binary and runs it as a subprocess")
	}
	if runtime.GOOS == "windows" {
		t.Skip("windows: the shutdown leg signals the daemon with SIGTERM, which Windows cannot deliver")
	}

	ctx, cancel := context.WithTimeout(context.Background(), journeyBudget)
	defer cancel()

	bin := buildTokenops(ctx, t)
	inst := newInstallation(t)

	// init. The config and the store must land inside the synthetic
	// installation: a binary that resolved paths from the developer's
	// real environment instead of HOME/XDG_* would pass every in-process
	// test and write to ~/.config/tokenops here.
	initOut := inst.run(ctx, t, bin, "init")
	if !strings.Contains(initOut, inst.configPath()) {
		t.Fatalf("init did not report writing %s:\n%s", inst.configPath(), initOut)
	}
	if _, err := os.Stat(inst.configPath()); err != nil {
		t.Fatalf("init wrote no config at the XDG path: %v", err)
	}
	inst.disablePricingRefresh(t)

	// Enable the ingestion source the way an operator does, through the
	// command — not by hand-writing YAML, which is precisely the wiring
	// this test exists to exercise. --no-restart because the restart path
	// bounces a supervised unit, and this machine's real daemon is not
	// ours to touch.
	inst.run(ctx, t, bin, "vendor-usage", "enable", "claude-code-jsonl",
		"--interval", "1s", "--no-restart")

	before := transcriptTurn{
		messageID: "msg_e2e_before_boot",
		model:     "claude-sonnet-4-5",
		prompt:    "summarise the failing test",
		input:     1200,
		output:    340,
		cacheRead: 8000,
		at:        time.Now().UTC().Add(-time.Minute),
	}
	inst.appendTurn(t, before)

	daemon := inst.startDaemon(t, bin)
	hint := daemon.waitForHint(ctx, t, inst)
	waitReady(ctx, t, hint.Addr)

	after := transcriptTurn{
		messageID: "msg_e2e_after_ready",
		model:     "claude-sonnet-4-5",
		prompt:    "now apply the fix",
		input:     900,
		output:    120,
		cacheRead: 4000,
		at:        time.Now().UTC(),
	}
	inst.appendTurn(t, after)

	wantTokens := before.totalTokens() + after.totalTokens()

	// Query surface 1 — the daemon's HTTP API, credentialed with the
	// token the daemon itself published in its URL hint.
	waitFor(ctx, t, "both transcript turns to reach /api/spend/summary", func() (bool, string) {
		var body struct {
			Summary struct {
				Requests    int64 `json:"Requests"`
				TotalTokens int64 `json:"TotalTokens"`
			} `json:"summary"`
		}
		getJSON(ctx, t, hint, "/api/spend/summary?since=1h", &body)
		return body.Summary.Requests >= 2 && body.Summary.TotalTokens >= wantTokens,
			fmt.Sprintf("requests=%d tokens=%d (want >=2, >=%d)",
				body.Summary.Requests, body.Summary.TotalTokens, wantTokens)
	})

	// Reconstruct. The transcript's session becomes a workflow trace with
	// one step per assistant turn — the step where telemetry stops being
	// rows and becomes something that happened.
	var detail struct {
		Trace struct {
			WorkflowID       string `json:"WorkflowID"`
			StepCount        int    `json:"StepCount"`
			TotalTotalTokens int64  `json:"TotalTotalTokens"`
		} `json:"trace"`
	}
	getJSON(ctx, t, hint, "/api/workflows/"+url.PathEscape(inst.workflowID()), &detail)
	if detail.Trace.WorkflowID != inst.workflowID() {
		t.Errorf("reconstructed workflow = %q, want %q", detail.Trace.WorkflowID, inst.workflowID())
	}
	if detail.Trace.StepCount != 2 {
		t.Errorf("reconstructed trace has %d steps, want 2 (one per assistant turn)", detail.Trace.StepCount)
	}
	if detail.Trace.TotalTotalTokens != wantTokens {
		t.Errorf("reconstructed trace totals %d tokens, want %d", detail.Trace.TotalTotalTokens, wantTokens)
	}

	// Query surface 2 — the CLI, reading the same store off disk while
	// the daemon holds it open. This is the surface an operator checks
	// when they suspect ingestion has stopped, so it is the one that has
	// to agree with reality.
	var status struct {
		Sources []struct {
			SourceTag   string `json:"source_tag"`
			Enabled     bool   `json:"enabled"`
			EventsInWin int64  `json:"events_in_window"`
		} `json:"sources"`
	}
	out := inst.run(ctx, t, bin, "vendor-usage", "status", "--json", "--window", "1h")
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("decode vendor-usage status --json: %v\n%s", err, out)
	}
	var seen bool
	for _, s := range status.Sources {
		if s.SourceTag != "claude-code-jsonl" {
			continue
		}
		seen = true
		if !s.Enabled {
			t.Errorf("vendor-usage status reports claude-code-jsonl disabled after `vendor-usage enable`")
		}
		if s.EventsInWin < 2 {
			t.Errorf("vendor-usage status reports %d events in the last hour, want >= 2", s.EventsInWin)
		}
	}
	if !seen {
		t.Errorf("vendor-usage status --json listed no claude-code-jsonl source:\n%s", out)
	}

	daemon.shutdown(t)

	// A clean exit removes the URL hint. Leaving it behind is how every
	// surface keeps reporting a daemon that is no longer running.
	if _, err := os.Stat(inst.hintPath()); !os.IsNotExist(err) {
		t.Errorf("daemon.url still present after shutdown (stat err = %v)", err)
	}
}

// --- the installation ----------------------------------------------------

// installation is a throwaway tokenops installation: a synthetic home
// with its own XDG directories and an environment scrubbed of everything
// the binary could use to find the developer's real one.
type installation struct {
	home      string
	workdir   string
	env       []string
	sessionID string
	project   string
}

func newInstallation(t *testing.T) *installation {
	t.Helper()
	root := t.TempDir()
	in := &installation{
		home:      filepath.Join(root, "home"),
		workdir:   filepath.Join(root, "project"),
		sessionID: "11111111-2222-3333-4444-555555555555",
		// Claude Code encodes a project root by replacing separators
		// with dashes; the reader takes the directory name verbatim.
		project: "-e2e-tokenops-project",
	}
	for _, dir := range []string{in.home, in.workdir, in.xdgConfig(), in.xdgData(), filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	// The environment is built from nothing rather than filtered from
	// os.Environ(). A developer's shell exports TOKENOPS_* credentials
	// and XDG paths; inheriting either would make this test read and
	// write their real installation, and the failure would look like a
	// product bug rather than a test one.
	in.env = []string{
		"HOME=" + in.home,
		"XDG_CONFIG_HOME=" + in.xdgConfig(),
		"XDG_DATA_HOME=" + in.xdgData(),
		"XDG_STATE_HOME=" + filepath.Join(in.home, ".local", "state"),
		"XDG_CACHE_HOME=" + filepath.Join(in.home, ".cache"),
		"TMPDIR=" + filepath.Join(root, "tmp"),
		"PATH=" + os.Getenv("PATH"),
	}
	return in
}

func (in *installation) xdgConfig() string { return filepath.Join(in.home, ".config") }
func (in *installation) xdgData() string   { return filepath.Join(in.home, ".local", "share") }

func (in *installation) configPath() string {
	return filepath.Join(in.xdgConfig(), "tokenops", "config.yaml")
}

func (in *installation) hintPath() string {
	return filepath.Join(in.xdgData(), "tokenops", "daemon.url")
}

func (in *installation) transcriptPath() string {
	return filepath.Join(in.home, ".claude", "projects", in.project, in.sessionID+".jsonl")
}

// workflowID is how the poller stamps this transcript's session, and so
// the id the reconstruction has to answer to.
func (in *installation) workflowID() string {
	return "claude-code:" + in.project + ":" + in.sessionID
}

// run executes one tokenops subcommand to completion.
func (in *installation) run(ctx context.Context, t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = in.env
	cmd.Dir = in.workdir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("tokenops %s: %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// disablePricingRefresh switches off the daemon's one outbound call.
//
// The rate-card refresh fetches a public JSON file on boot when the local
// card is stale, which it always is in a fresh installation. It is the
// right default for a product and the wrong one for a test: this run must
// not depend on, or reach, anything off this machine.
func (in *installation) disablePricingRefresh(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(in.configPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	pricing, _ := cfg["pricing"].(map[string]any)
	if pricing == nil {
		pricing = map[string]any{}
	}
	pricing["refresh"] = map[string]any{"disabled": true}
	cfg["pricing"] = pricing
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(in.configPath(), out, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// transcriptTurn is one operator prompt and the assistant turn that
// answered it, in the shape Claude Code writes to its session JSONL.
type transcriptTurn struct {
	messageID string
	model     string
	prompt    string
	input     int64
	output    int64
	cacheRead int64
	at        time.Time
}

// totalTokens mirrors the poller's accounting: cache reads are input.
func (tt transcriptTurn) totalTokens() int64 {
	return tt.input + tt.cacheRead + tt.output
}

// appendTurn writes one prompt/response pair to the session transcript,
// appending so a turn added after boot exercises the tailing reader
// rather than a re-read of the whole file.
func (in *installation) appendTurn(t *testing.T, tt transcriptTurn) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(in.transcriptPath()), 0o700); err != nil {
		t.Fatalf("mkdir transcript dir: %v", err)
	}
	lines := []map[string]any{
		{
			"type":      "user",
			"timestamp": tt.at.Add(-2 * time.Second).Format(time.RFC3339Nano),
			"sessionId": in.sessionID,
			"message":   map[string]any{"content": tt.prompt},
		},
		{
			"type":      "assistant",
			"timestamp": tt.at.Format(time.RFC3339Nano),
			"sessionId": in.sessionID,
			"message": map[string]any{
				"id":    tt.messageID,
				"model": tt.model,
				"usage": map[string]any{
					"input_tokens":                tt.input,
					"output_tokens":               tt.output,
					"cache_read_input_tokens":     tt.cacheRead,
					"cache_creation_input_tokens": 0,
					"service_tier":                "standard",
				},
			},
		},
	}
	f, err := os.OpenFile(in.transcriptPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer func() { _ = f.Close() }()
	for _, line := range lines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("encode transcript line: %v", err)
		}
		if _, err := f.Write(append(encoded, '\n')); err != nil {
			t.Fatalf("write transcript line: %v", err)
		}
	}
}

// --- the daemon subprocess ----------------------------------------------

// daemonProc is the `tokenops start` subprocess. Everything here acts on
// one PID: this machine runs other tokenops daemons, and a test that
// matched processes by name would stop them.
type daemonProc struct {
	cmd    *exec.Cmd
	stderr *lockedBuffer
	exited chan error
}

// startDaemon launches the daemon on an ephemeral port. Port 0 rather
// than the configured default because a fixed port would collide with
// whatever else is listening on this machine, and the collision would
// read as a product failure.
func (in *installation) startDaemon(t *testing.T, bin string) *daemonProc {
	t.Helper()
	cmd := exec.Command(bin, "start", "--listen", "127.0.0.1:0")
	cmd.Env = in.env
	cmd.Dir = in.workdir
	buf := &lockedBuffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	d := &daemonProc{cmd: cmd, stderr: buf, exited: make(chan error, 1)}
	go func() { d.exited <- cmd.Wait() }()
	t.Cleanup(func() {
		// Last resort only: the test signals the daemon itself and
		// asserts on how it exits. Kill targets the PID we started and
		// nothing else.
		select {
		case <-d.exited:
			return
		default:
		}
		_ = cmd.Process.Kill()
	})
	return d
}

// waitForHint waits for the daemon to publish its listen address, and
// checks the hint belongs to this process. Reading another daemon's hint
// would point the whole test at someone else's store — on a developer
// machine that is the likely failure, not the unlikely one.
func (d *daemonProc) waitForHint(ctx context.Context, t *testing.T, in *installation) urlHint {
	t.Helper()
	var hint urlHint
	waitFor(ctx, t, "the daemon to publish its URL hint", func() (bool, string) {
		data, err := os.ReadFile(in.hintPath())
		if err != nil {
			return false, err.Error()
		}
		if err := json.Unmarshal(data, &hint); err != nil {
			return false, "hint not yet parseable: " + err.Error()
		}
		return hint.Addr != "" && hint.PID == d.cmd.Process.Pid,
			fmt.Sprintf("addr=%q pid=%d (want pid %d)", hint.Addr, hint.PID, d.cmd.Process.Pid)
	})
	if hint.DashboardToken == "" {
		t.Fatal("daemon published no dashboard token; every /api query below would be a 401")
	}
	return hint
}

// shutdown signals the daemon and requires it to exit cleanly.
func (d *daemonProc) shutdown(t *testing.T) {
	t.Helper()
	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal daemon: %v", err)
	}
	select {
	case err := <-d.exited:
		if err != nil {
			t.Errorf("daemon exited %v after SIGTERM\nlog:\n%s", err, d.stderr.String())
		}
	case <-time.After(shutdownBudget):
		_ = d.cmd.Process.Kill()
		t.Fatalf("daemon did not exit within %s of SIGTERM\nlog:\n%s", shutdownBudget, d.stderr.String())
	}
}

// urlHint is the subset of ~/.local/share/tokenops/daemon.url this test
// reads: where the daemon listens, which process wrote it, and the
// credential the API is gated on.
type urlHint struct {
	Addr           string `json:"addr"`
	PID            int    `json:"pid"`
	DashboardToken string `json:"dashboard_token"`
}

// lockedBuffer collects the daemon's log. os/exec writes to it from its
// own goroutine while the test reads it for failure output.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// --- helpers -------------------------------------------------------------

// buildTokenops compiles the binary under test. Building by import path
// rather than a relative one keeps this independent of where the test
// package sits in the tree.
func buildTokenops(ctx context.Context, t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tokenops")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, "go.klarlabs.de/tokenops/cmd/tokenops")
	// The build inherits the real environment on purpose: it needs the
	// module and build caches. Only the binary under test runs scrubbed.
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build cmd/tokenops: %v\n%s", err, out)
	}
	return bin
}

// waitReady polls /readyz until the daemon reports itself ready. The
// readiness endpoint rather than a sleep: a sleep encodes how fast this
// machine happened to be when the test was written.
func waitReady(ctx context.Context, t *testing.T, addr string) {
	t.Helper()
	waitFor(ctx, t, "the daemon to report ready on /readyz", func() (bool, string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/readyz", nil)
		if err != nil {
			return false, err.Error()
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false, err.Error()
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode == http.StatusOK, fmt.Sprintf("status=%d body=%s", resp.StatusCode, bytes.TrimSpace(body))
	})
}

// getJSON performs one credentialed GET against the running daemon.
func getJSON(ctx context.Context, t *testing.T, hint urlHint, path string, out any) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+hint.Addr+path, nil)
	if err != nil {
		t.Fatalf("build request %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+hint.DashboardToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode %s: %v\n%s", path, err, body)
	}
}

// waitFor polls cond until it holds or the budget runs out, failing with
// whatever cond last observed rather than a bare timeout.
func waitFor(ctx context.Context, t *testing.T, what string, cond func() (bool, string)) {
	t.Helper()
	var last string
	for {
		ok, detail := cond()
		if ok {
			return
		}
		last = detail
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s; last observation: %s", what, last)
		case <-time.After(pollInterval):
		}
	}
}
