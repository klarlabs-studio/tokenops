package teamclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// AdminState is an administrator's API credential for the CLI, kept apart
// from the enrolment: an owner may administer from a machine that uploads
// nothing, and leaving a team must not leave an admin token behind.
type AdminState struct {
	URL       string    `json:"url"`
	OrgName   string    `json:"org_name"`
	Role      string    `json:"role"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AdminStatePath is team-admin.json beside the enrolment's state file.
func AdminStatePath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "team-admin.json")
}

// ErrNoAdmin is returned when this machine holds no administrator's
// credential, or it expired.
var ErrNoAdmin = errors.New("no administrator credential on this machine (tokenops team admin login)")

// LoadAdmin reads the administrator's credential at path. An expired one
// is ErrNoAdmin.
func LoadAdmin(path string, now time.Time) (AdminState, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the configured state directory
	if errors.Is(err, os.ErrNotExist) {
		return AdminState{}, ErrNoAdmin
	}
	if err != nil {
		return AdminState{}, err
	}
	var s AdminState
	if err := json.Unmarshal(raw, &s); err != nil {
		return AdminState{}, fmt.Errorf("team admin state %s: %w", path, err)
	}
	if s.URL == "" || s.Token == "" || (!s.ExpiresAt.IsZero() && !now.Before(s.ExpiresAt)) {
		return AdminState{}, ErrNoAdmin
	}
	return s, nil
}

// SaveAdmin writes the credential readable by this user only, atomically.
func SaveAdmin(path string, s AdminState) error {
	return writePrivateJSON(path, s)
}

// IsCode reports whether err is a refusal the server marked with code.
func IsCode(err error, code string) bool {
	var se *ServerError
	return errors.As(err, &se) && se.Code == code
}

// StartDeviceLink begins linking this machine in the browser.
func (c *Client) StartDeviceLink(ctx context.Context, req teamwire.DeviceLinkRequest) (teamwire.DeviceLink, error) {
	var out teamwire.DeviceLink
	err := c.do(ctx, http.MethodPost, "/api/v1/device-links", req, &out)
	return out, err
}

// PollDeviceLink asks once whether the link was approved. While it is not,
// the error carries teamwire.CodeAuthorizationPending.
func (c *Client) PollDeviceLink(ctx context.Context, deviceCode string) (teamwire.DeviceLinkResult, error) {
	var out teamwire.DeviceLinkResult
	err := c.do(ctx, http.MethodPost, "/api/v1/device-links/token", teamwire.DeviceLinkPoll{DeviceCode: deviceCode}, &out)
	return out, err
}

// WaitDeviceLink polls until the link is approved, declined or expired, or
// ctx ends, waiting the server's interval between polls and longer when
// told to slow down. sleep is time.Sleep's stand-in for tests; nil waits
// on ctx.
func (c *Client) WaitDeviceLink(ctx context.Context, link teamwire.DeviceLink, sleep func(context.Context, time.Duration) error) (teamwire.DeviceLinkResult, error) {
	if sleep == nil {
		sleep = sleepCtx
	}
	interval := time.Duration(link.Interval) * time.Second
	if interval < time.Second {
		interval = 5 * time.Second
	}
	for {
		if err := sleep(ctx, interval); err != nil {
			return teamwire.DeviceLinkResult{}, err
		}
		res, err := c.PollDeviceLink(ctx, link.DeviceCode)
		switch {
		case err == nil:
			return res, nil
		case IsCode(err, teamwire.CodeAuthorizationPending):
		case IsCode(err, teamwire.CodeSlowDown):
			interval += 5 * time.Second
		case IsCode(err, teamwire.CodeExpiredToken):
			return res, errors.New("the code expired before it was approved; run the command again")
		case IsCode(err, teamwire.CodeAccessDenied):
			return res, errors.New("the link was declined in the browser")
		default:
			return res, err
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// RevokeSelf revokes the credential the client holds.
func (c *Client) RevokeSelf(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/tokens/self", nil, nil)
}

// Teams lists the organisation's teams.
func (c *Client) Teams(ctx context.Context) ([]teamwire.Team, error) {
	var out []teamwire.Team
	err := c.do(ctx, http.MethodGet, "/api/v1/teams", nil, &out)
	return out, err
}

// CreateTeam creates a team.
func (c *Client) CreateTeam(ctx context.Context, name string) (teamwire.Team, error) {
	var out teamwire.Team
	err := c.do(ctx, http.MethodPost, "/api/v1/teams", teamwire.CreateTeamRequest{Name: name}, &out)
	return out, err
}

// Invite creates a single-use invite.
func (c *Client) Invite(ctx context.Context, req teamwire.InviteRequest) (teamwire.Invite, error) {
	var out teamwire.Invite
	err := c.do(ctx, http.MethodPost, "/api/v1/invites", req, &out)
	return out, err
}

// Members lists the organisation's members.
func (c *Client) Members(ctx context.Context) ([]teamwire.Member, error) {
	var out []teamwire.Member
	err := c.do(ctx, http.MethodGet, "/api/v1/members", nil, &out)
	return out, err
}

// RemoveMember removes a member and erases their figures.
func (c *Client) RemoveMember(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/members/"+url.PathEscape(id), nil, nil)
}

// SetRole changes a member's role.
func (c *Client) SetRole(ctx context.Context, id, role string) error {
	return c.do(ctx, http.MethodPut, "/api/v1/members/"+url.PathEscape(id)+"/role", teamwire.RoleRequest{Role: role}, nil)
}

// Grants lists the organisation's grants, revoked ones included.
func (c *Client) Grants(ctx context.Context) ([]teamwire.Grant, error) {
	var out []teamwire.Grant
	err := c.do(ctx, http.MethodGet, "/api/v1/grants", nil, &out)
	return out, err
}

// Grant lets a member see individual figures.
func (c *Client) Grant(ctx context.Context, req teamwire.GrantRequest) (teamwire.Created, error) {
	var out teamwire.Created
	err := c.do(ctx, http.MethodPost, "/api/v1/grants", req, &out)
	return out, err
}

// RevokeGrant revokes a grant.
func (c *Client) RevokeGrant(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/grants/"+url.PathEscape(id), nil, nil)
}

// Billing reads the organisation's billing.
func (c *Client) Billing(ctx context.Context) (teamwire.Billing, error) {
	var out teamwire.Billing
	err := c.do(ctx, http.MethodGet, "/api/v1/billing", nil, &out)
	return out, err
}

// BillingCheckout asks for a link to subscribe.
func (c *Client) BillingCheckout(ctx context.Context) (teamwire.BillingLink, error) {
	var out teamwire.BillingLink
	err := c.do(ctx, http.MethodPost, "/api/v1/billing/checkout", nil, &out)
	return out, err
}

// BillingPortal asks for a link to the payment provider's customer portal.
func (c *Client) BillingPortal(ctx context.Context) (teamwire.BillingLink, error) {
	var out teamwire.BillingLink
	err := c.do(ctx, http.MethodPost, "/api/v1/billing/portal", nil, &out)
	return out, err
}

// Audit reads the organisation's audit log, newest first.
func (c *Client) Audit(ctx context.Context) ([]teamwire.AuditEntry, error) {
	var out []teamwire.AuditEntry
	err := c.do(ctx, http.MethodGet, "/api/v1/audit", nil, &out)
	return out, err
}
