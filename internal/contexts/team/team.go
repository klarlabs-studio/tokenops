// Package team holds the rules of the team plane (ADR 0012): who may see
// what, how figures add up, when a group is too small to show, how
// credentials are minted and stored, and how long figures are kept.
//
// It is pure policy. The server's Postgres store and HTTP handlers
// (internal/teamserver) apply it; nothing here does I/O.
package team

import (
	"fmt"
	"time"
)

// Role is what a member may do in an organisation. Roles grant
// administration; they never grant sight of an individual's figures on
// their own — that takes an explicit Grant, which the member can see.
type Role string

// The roles, least privileged first.
const (
	RoleMember Role = "member"
	// RoleLead may be granted to view the individual figures of a team.
	RoleLead Role = "lead"
	// RoleAdmin creates teams and invites.
	RoleAdmin Role = "admin"
	// RoleOwner also grants and revokes individual views and sets the
	// organisation's privacy settings.
	RoleOwner Role = "owner"
)

var roleRank = map[Role]int{RoleMember: 0, RoleLead: 1, RoleAdmin: 2, RoleOwner: 3}

// ParseRole reads a role name.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if _, ok := roleRank[r]; !ok {
		return "", fmt.Errorf("role %q: want member, lead, admin or owner", s)
	}
	return r, nil
}

// AtLeast reports whether r is at least as privileged as other.
func (r Role) AtLeast(other Role) bool { return roleRank[r] >= roleRank[other] }

// CanAdminister reports whether r may create teams and invites and read the
// audit log.
func (r Role) CanAdminister() bool { return r.AtLeast(RoleAdmin) }

// CanGrant reports whether r may grant or revoke individual views.
func (r Role) CanGrant() bool { return r == RoleOwner }

// CanHoldGrant reports whether r may be granted individual views. A plain
// member never is: seeing a colleague's figures is a management function,
// and one the colleague is told about.
func (r Role) CanHoldGrant() bool { return r.AtLeast(RoleLead) }

// CanInvite reports whether inviter may invite someone with role invited:
// nobody hands out more than they hold, and only an owner makes an owner.
func CanInvite(inviter, invited Role) bool {
	return inviter.CanAdminister() && inviter.AtLeast(invited)
}

// Grant lets one member see the individual figures of the members of a
// team, or of everyone in the organisation when TeamID is empty.
type Grant struct {
	ID        string
	GranteeID string
	TeamID    string
	RevokedAt *time.Time
}

// Active reports whether the grant is in force.
func (g Grant) Active() bool { return g.RevokedAt == nil }

// Covers reports whether the grant reaches a member of subjectTeams.
func (g Grant) Covers(subjectTeams []string) bool {
	if !g.Active() {
		return false
	}
	if g.TeamID == "" {
		return true
	}
	for _, t := range subjectTeams {
		if t == g.TeamID {
			return true
		}
	}
	return false
}

// Access is the answer to "may this viewer see this member's figures".
type Access struct {
	Allowed bool
	// Self is set when the viewer is the member.
	Self bool
	// GrantID is the grant that allows it, recorded with the view.
	GrantID string
}

// CanViewMember decides whether viewer may see subject's individual
// figures. Members always see their own. Anyone else needs an active grant
// that reaches one of the subject's teams, and a role that may hold one —
// a demoted lead's old grant stops working without being revoked.
func CanViewMember(viewerID string, viewerRole Role, subjectID string, subjectTeams []string, grants []Grant) Access {
	if viewerID == subjectID {
		return Access{Allowed: true, Self: true}
	}
	if !viewerRole.CanHoldGrant() {
		return Access{}
	}
	for _, g := range grants {
		if g.GranteeID == viewerID && g.Covers(subjectTeams) {
			return Access{Allowed: true, GrantID: g.ID}
		}
	}
	return Access{}
}
