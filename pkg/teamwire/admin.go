package teamwire

import "time"

// This file is the self-serve half of the contract: linking a machine to
// an account in the browser, administering an organisation, and its
// billing. None of it is part of the upload. These messages may carry
// names, e-mail addresses and the reasons an owner gives for a grant —
// what an administrator types — but never a figure's free text: the
// figures cross only in an Upload.

// Roles a member can hold, least privileged first.
const (
	RoleMember = "member"
	RoleLead   = "lead"
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
)

// Roles lists every role, least privileged first.
var Roles = []string{RoleMember, RoleLead, RoleAdmin, RoleOwner}

// ValidRole reports whether s is a role.
func ValidRole(s string) bool {
	for _, r := range Roles {
		if s == r {
			return true
		}
	}
	return false
}

// Error codes a server sets in Error.Code when a client must act on the
// refusal rather than show it.
const (
	// CodeUploadsPaused: the organisation has no active trial or
	// subscription, so uploads are refused (402). The figures already held
	// stay viewable, read-only, until Plan.PurgeAt.
	CodeUploadsPaused = "uploads_paused"
	// CodeAuthorizationPending: a device link was not approved in the
	// browser yet; poll again after the interval.
	CodeAuthorizationPending = "authorization_pending"
	// CodeSlowDown: a device link was polled faster than its interval.
	CodeSlowDown = "slow_down"
	// CodeExpiredToken: the device link expired before it was approved.
	CodeExpiredToken = "expired_token"
	// CodeAccessDenied: the device link was declined in the browser.
	CodeAccessDenied = "access_denied"
)

// DeviceLinkRequest starts linking this machine to an account in the
// browser, as a device authorization grant does (RFC 8628): the server
// answers with a short code the person types into a page they are signed
// in to, and this machine polls until they approve it there.
type DeviceLinkRequest struct {
	// Enroll asks for this machine to be enrolled as a device of the
	// approving member, as an invite would.
	Enroll bool `json:"enroll"`
	// Admin asks for an administrator's API credential for the CLI. The
	// server grants it only when the approving member is an owner or admin.
	Admin bool `json:"admin"`
	// Create sends the browser through sign-up and creating an
	// organisation when the person has none.
	Create bool `json:"create,omitempty"`
	// DisplayName is the member's name for an organisation created during
	// the link; an existing member keeps theirs.
	DisplayName string `json:"display_name,omitempty"`
	// DeviceName tells the member's machines apart; required with Enroll.
	DeviceName string `json:"device_name,omitempty"`
}

// DeviceLink is a started link. DeviceCode is the machine's secret: it is
// only ever sent back to poll, and the server keeps its hash. UserCode is
// what the person types in the browser; it is never put in the URL, so a
// link someone else sends cannot approve their machine instead.
type DeviceLink struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	// VerificationURL is the page to type UserCode into.
	VerificationURL string    `json:"verification_url"`
	ExpiresAt       time.Time `json:"expires_at"`
	// Interval is how many seconds to wait between polls.
	Interval int `json:"interval"`
}

// DeviceLinkPoll asks whether a link was approved.
type DeviceLinkPoll struct {
	DeviceCode string `json:"device_code"`
}

// DeviceLinkResult is an approved link, handed out once. Enrollment is set
// when enrolment was asked for, Admin when an administrator's credential
// was asked for and the approver may hold one.
type DeviceLinkResult struct {
	Enrollment *EnrollResponse  `json:"enrollment,omitempty"`
	Admin      *AdminCredential `json:"admin,omitempty"`
}

// AdminCredential is an owner's or admin's API token for the CLI. It
// expires; the server keeps only its hash.
type AdminCredential struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	OrgName   string    `json:"org_name"`
	Role      string    `json:"role"`
}

// Team is one team of an organisation.
type Team struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Members int    `json:"members"`
}

// CreateTeamRequest creates a team.
type CreateTeamRequest struct {
	Name string `json:"name"`
}

// Member is a member as an administrator lists them.
type Member struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Role  string   `json:"role"`
	Teams []string `json:"teams"`
	// Email is the member's sign-in address, shown to administrators only.
	Email string `json:"email,omitempty"`
	// CanView is set when the caller may see this member's figures.
	CanView bool `json:"can_view"`
	// IsYou marks the caller.
	IsYou bool `json:"is_you,omitempty"`
}

// InviteRequest creates a single-use invite.
type InviteRequest struct {
	// Team is the team's name or ID.
	Team string `json:"team"`
	// Role defaults to member; nobody invites above their own role.
	Role string `json:"role,omitempty"`
	// TTLHours defaults to seven days.
	TTLHours int `json:"ttl_hours,omitempty"`
	// Email, when set, is sent the invite: the join line and a page that
	// explains installing TokenOps. The invite stays single-use.
	Email string `json:"email,omitempty"`
}

// Invite is a created invite, shown once.
type Invite struct {
	Invite    string    `json:"invite"`
	ExpiresAt time.Time `json:"expires_at"`
	// Join is the command the invited person runs.
	Join string `json:"join"`
	// Emailed is set when the invite was sent to InviteRequest.Email.
	Emailed bool `json:"emailed,omitempty"`
}

// RoleRequest changes a member's role.
type RoleRequest struct {
	Role string `json:"role"`
}

// GrantRequest lets a lead, admin or owner see individual figures: of one
// team's members, or of everyone when Team is empty. The member reads
// Reason.
type GrantRequest struct {
	GranteeID string `json:"grantee_id"`
	Team      string `json:"team,omitempty"`
	Reason    string `json:"reason"`
}

// Grant is a grant as listed.
type Grant struct {
	ID        string     `json:"id"`
	Grantee   string     `json:"grantee"`
	GranteeID string     `json:"grantee_id"`
	Scope     string     `json:"scope"`
	Reason    string     `json:"reason"`
	GrantedBy string     `json:"granted_by"`
	GrantedAt time.Time  `json:"granted_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// Created answers a request that created something with its ID.
type Created struct {
	ID string `json:"id"`
}

// AuditEntry is one line of an organisation's audit log.
type AuditEntry struct {
	At      time.Time `json:"at"`
	Actor   string    `json:"actor"`
	Action  string    `json:"action"`
	Subject string    `json:"subject,omitempty"`
	Detail  string    `json:"detail,omitempty"`
}

// Billing states of an organisation.
const (
	// PlanTrialing: within the free trial; uploads are accepted.
	PlanTrialing = "trialing"
	// PlanActive: a paid subscription; uploads are accepted.
	PlanActive = "active"
	// PlanPastDue: a payment failed and is being retried; uploads are
	// accepted while the payment provider retries.
	PlanPastDue = "past_due"
	// PlanPaused: the trial ended or the subscription was canceled;
	// uploads are refused and the figures held are kept, read-only, until
	// Plan.PurgeAt.
	PlanPaused = "paused"
)

// Plan is the organisation's billing state as every member sees it.
type Plan struct {
	Status string `json:"status"`
	// TrialEndsAt is when the free trial ends (set while trialing).
	TrialEndsAt *time.Time `json:"trial_ends_at,omitempty"`
	// UploadsPaused is set when ingest refuses uploads.
	UploadsPaused bool `json:"uploads_paused"`
	// PurgeAt is when the figures are deleted unless a subscription
	// starts (set while paused).
	PurgeAt *time.Time `json:"purge_at,omitempty"`
	// FirstWeekAt is when the organisation's first week of totals is
	// released: three days after the first week with uploads ends. Unset
	// once a week was released.
	FirstWeekAt *time.Time `json:"first_week_at,omitempty"`
}

// Billing is the organisation's billing as an owner or admin sees it.
type Billing struct {
	Plan
	// Seats is the billed quantity: the organisation's active members.
	Seats int `json:"seats"`
	// CurrentPeriodEnd is when the subscription renews.
	CurrentPeriodEnd *time.Time `json:"current_period_end,omitempty"`
	// CancelAt is set when the subscription ends at the period's end.
	CancelAt *time.Time `json:"cancel_at,omitempty"`
	// ManageURL is the web page where an owner subscribes or opens the
	// payment provider's customer portal.
	ManageURL string `json:"manage_url"`
}

// BillingLink is a link into the payment provider: a checkout to
// subscribe, or the customer portal to change payment details, see
// invoices or cancel.
type BillingLink struct {
	URL string `json:"url"`
}
