// Package teamclient is this machine's side of the team plane (ADR 0012):
// the enrolment it keeps in a 0600 state file, and the HTTPS client that
// enrols, uploads derived figures and leaves. It never logs or prints the
// device token.
package teamclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// State is a joined machine's enrolment.
type State struct {
	URL         string    `json:"url"`
	OrgName     string    `json:"org_name"`
	TeamName    string    `json:"team_name"`
	MemberID    string    `json:"member_id"`
	DeviceID    string    `json:"device_id"`
	DeviceToken string    `json:"device_token"`
	JoinedAt    time.Time `json:"joined_at"`
	// LastUpload and LastResult record the uploader's latest attempt.
	LastUpload *time.Time `json:"last_upload,omitempty"`
	LastResult string     `json:"last_result,omitempty"`
}

// DefaultStatePath is ~/.tokenops/team.json.
func DefaultStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tokenops", "team.json"), nil
}

// ResolveStatePath is configured, or the default.
func ResolveStatePath(configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	return DefaultStatePath()
}

// ErrNotJoined is returned when this machine has joined no team.
var ErrNotJoined = errors.New("this machine has not joined a team (tokenops team join <url> <invite>)")

// Load reads the enrolment at path.
func Load(path string) (State, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the configured state file
	if errors.Is(err, os.ErrNotExist) {
		return State{}, ErrNotJoined
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}, fmt.Errorf("team state %s: %w", path, err)
	}
	if s.URL == "" || s.DeviceToken == "" {
		return State{}, ErrNotJoined
	}
	return s, nil
}

// Save writes the enrolment readable by this user only, atomically.
func Save(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".team-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Remove deletes the enrolment.
func Remove(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// CheckURL accepts an https URL, or http to this machine only (for a
// server run locally); anything else would send the device token in clear.
func CheckURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not a server URL; want https://team.example.eu", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		host := u.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", fmt.Errorf("%s: a team server is reached over https (http only to localhost)", raw)
		}
	default:
		return "", fmt.Errorf("%s: want an https URL", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("%s: a server URL has no query, fragment or credentials", raw)
	}
	return u.String(), nil
}

// Client talks to one team server.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New returns a client with a bounded timeout.
func New(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// ServerError is a refusal the server explained.
type ServerError struct {
	Status  int
	Message string
	Hint    string
}

func (e *ServerError) Error() string {
	msg := fmt.Sprintf("team server answered %d: %s", e.Status, e.Message)
	if e.Hint != "" {
		msg += " (" + e.Hint + ")"
	}
	return msg
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var body teamwire.Error
		se := &ServerError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		if json.Unmarshal(raw, &body) == nil && body.Error != "" {
			se.Message, se.Hint = body.Error, body.Hint
		}
		return se
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Enroll redeems an invite for a device token.
func (c *Client) Enroll(ctx context.Context, req teamwire.EnrollRequest) (teamwire.EnrollResponse, error) {
	var out teamwire.EnrollResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/enroll", req, &out)
	return out, err
}

// Upload sends one upload.
func (c *Client) Upload(ctx context.Context, u teamwire.Upload) (teamwire.IngestResponse, error) {
	var out teamwire.IngestResponse
	err := c.do(ctx, http.MethodPost, "/api/v1/ingest", u, &out)
	return out, err
}

// Me reads what the server holds about this member and who can see it.
func (c *Client) Me(ctx context.Context) (teamwire.Me, error) {
	var out teamwire.Me
	err := c.do(ctx, http.MethodGet, "/api/v1/me", nil, &out)
	return out, err
}

// LoginLink asks for a single-use web sign-in link.
func (c *Client) LoginLink(ctx context.Context) (teamwire.LoginLink, error) {
	var out teamwire.LoginLink
	err := c.do(ctx, http.MethodPost, "/api/v1/login-links", nil, &out)
	return out, err
}

// Leave revokes this device; its figures are erased unless keep.
func (c *Client) Leave(ctx context.Context, keep bool) error {
	path := "/api/v1/devices/self"
	if keep {
		path += "?keep=true"
	}
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
