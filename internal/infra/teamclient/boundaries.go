package teamclient

import (
	"context"
	"net/http"
	"net/url"

	"go.klarlabs.de/tokenops/pkg/teamwire"
)

// The boundaries half of the API (ADR 0014 §4–5). A machine reads its
// organisation's rules and asks for approvals with its device token;
// owners, admins and leads author rules and answer with an admin token.

// Boundaries fetches the organisation's signed rules.
func (c *Client) Boundaries(ctx context.Context) (teamwire.SignedBoundaries, error) {
	var out teamwire.SignedBoundaries
	err := c.do(ctx, http.MethodGet, "/api/v1/boundaries", nil, &out)
	return out, err
}

// RequestApproval asks the organisation for the approval a rule requires.
// The request is validated before it is sent: it must be figures and
// enumerations only.
func (c *Client) RequestApproval(ctx context.Context, req teamwire.ApprovalRequest) (teamwire.Approval, error) {
	if err := req.Validate(); err != nil {
		return teamwire.Approval{}, err
	}
	var out teamwire.Approval
	err := c.do(ctx, http.MethodPost, "/api/v1/approvals", req, &out)
	return out, err
}

// ApprovalFor reads this machine's latest request for a decision.
func (c *Client) ApprovalFor(ctx context.Context, decisionID string) (teamwire.Approval, error) {
	var out teamwire.Approval
	err := c.do(ctx, http.MethodGet, "/api/v1/approvals/"+url.PathEscape(decisionID), nil, &out)
	return out, err
}

// OrgBoundaries reads the organisation's rules, unsigned, for display.
func (c *Client) OrgBoundaries(ctx context.Context) (teamwire.OrgBoundaries, error) {
	var out teamwire.OrgBoundaries
	err := c.do(ctx, http.MethodGet, "/api/v1/org-boundaries", nil, &out)
	return out, err
}

// SetOrgBoundaries replaces the organisation's rules (owners and admins).
func (c *Client) SetOrgBoundaries(ctx context.Context, rules []teamwire.BoundaryRule) (teamwire.OrgBoundaries, error) {
	if rules == nil {
		rules = []teamwire.BoundaryRule{}
	}
	var out teamwire.OrgBoundaries
	err := c.do(ctx, http.MethodPut, "/api/v1/org-boundaries", map[string]any{"rules": rules}, &out)
	return out, err
}

// Approvals lists the requests the caller may answer: pending ones, or
// with all, answered ones too.
func (c *Client) Approvals(ctx context.Context, all bool) ([]teamwire.Approval, error) {
	status := teamwire.ApprovalPending
	if all {
		status = "all"
	}
	var out []teamwire.Approval
	err := c.do(ctx, http.MethodGet, "/api/v1/approvals?status="+status, nil, &out)
	return out, err
}

// AnswerApproval approves or denies a request by its server id.
func (c *Client) AnswerApproval(ctx context.Context, id string, answer teamwire.ApprovalAnswer) (teamwire.Approval, error) {
	var out teamwire.Approval
	err := c.do(ctx, http.MethodPost, "/api/v1/approvals/"+url.PathEscape(id)+"/answer", answer, &out)
	return out, err
}
