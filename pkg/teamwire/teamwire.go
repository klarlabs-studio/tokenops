// Package teamwire is the contract between a TokenOps daemon and a team
// plane (ADR 0012): what a machine that joined a team uploads, and what
// enrolment exchanges.
//
// The upload is the privacy boundary. It carries derived figures only —
// counts, token totals, costs, durations — keyed by a UTC day, a repository
// label and a kind of work. It has no field that can hold a prompt, a file
// path, a transcript, a commit message or a model output: every string in
// it is either an enumeration or a label matched against a pattern that
// admits no whitespace and no path, and TestUploadHasNoFreeText fails the
// build when a field that could carry free text is added. The server
// decodes with unknown fields refused and runs the same Validate, so a
// modified client cannot widen the boundary either.
package teamwire

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"time"
)

// SchemaVersion is the upload format this package describes.
const SchemaVersion = 1

// Limits a server enforces on an upload.
const (
	// MaxUploadBytes bounds the request body.
	MaxUploadBytes = 1 << 20
	// MaxBuckets bounds the buckets in one upload.
	MaxBuckets = 5000
	// MaxDays bounds how many days one upload may cover.
	MaxDays = 62
)

// Kinds of work a bucket can be filed under. They are the task classifier's
// vocabulary (internal/contexts/optimization/taskclass); an instruction is
// classified on the machine and only the kind leaves it.
const (
	KindUnknown  = "unknown"
	KindLookup   = "lookup"
	KindResearch = "research"
	KindEdit     = "edit"
	KindDeep     = "deep"
)

// Kinds lists every valid kind, cheapest work first.
var Kinds = []string{KindUnknown, KindLookup, KindResearch, KindEdit, KindDeep}

// Repository labels that name no repository.
const (
	// RepoNone files work done outside any git repository.
	RepoNone = "none"
	// RepoHidden files work whose repository the member chose not to name.
	RepoHidden = "hidden"
)

// repoPattern admits "name" or "owner/name": letters, digits, dot, dash
// and underscore, at most one slash, no leading slash, no whitespace. It is
// what keeps a path or a sentence out of the one label that is not an enum.
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}(/[A-Za-z0-9][A-Za-z0-9._-]{0,63})?$`)

// labelPattern admits a short token such as a client version.
var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]{0,39}$`)

// uuidPattern admits a canonical, lower-case UUID.
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidRepo reports whether s may be sent as a repository label.
func ValidRepo(s string) bool { return repoPattern.MatchString(s) }

// ValidKind reports whether s is a kind of work.
func ValidKind(s string) bool {
	for _, k := range Kinds {
		if s == k {
			return true
		}
	}
	return false
}

// ValidLabel reports whether s is a short token (a client version).
func ValidLabel(s string) bool { return labelPattern.MatchString(s) }

// ValidUUID reports whether s is a canonical lower-case UUID.
func ValidUUID(s string) bool { return uuidPattern.MatchString(s) }

// DayLayout is how a day is written: a UTC calendar date.
const DayLayout = "2006-01-02"

// Upload is one machine's figures for the days it covers.
//
// An upload replaces, for each day in Days, everything the device sent for
// that day before: a day is recomputed whole on the machine and sent whole,
// so re-sending the same upload, or a later one for the same days, leaves
// the server with exactly one copy of the latest figures.
type Upload struct {
	Schema int `json:"schema"`
	// BatchID identifies this upload; a replay of the same batch is
	// acknowledged and not applied twice.
	BatchID string `json:"batch_id"`
	// ComputedAt is when the machine computed the figures. A server keeps
	// the newest computation of a day and ignores an older one that
	// arrives late.
	ComputedAt time.Time `json:"computed_at"`
	// ClientVersion is the TokenOps version that computed them.
	ClientVersion string `json:"client_version"`
	// Days are the UTC days this upload is complete for.
	Days []string `json:"days"`
	// Buckets are the figures, one per day, repository and kind of work.
	Buckets []Bucket `json:"buckets"`
}

// Bucket is the work one machine did on one UTC day, in one repository, of
// one kind. Every figure is a sum, so buckets add across machines, people,
// repositories and days; rates are derived from them after adding.
type Bucket struct {
	Day  string `json:"day"`
	Repo string `json:"repo"`
	Kind string `json:"kind"`

	// Sessions counts the agent sessions with work in the bucket.
	Sessions int64 `json:"sessions"`
	// Instructions counts the operator's instructions.
	Instructions int64 `json:"instructions"`
	// Turns and ToolCalls are the agent's turns and tool calls answering
	// them.
	Turns     int64 `json:"turns"`
	ToolCalls int64 `json:"tool_calls"`
	// ActiveSeconds is how long the operator waited on the agent, summed
	// over instructions.
	ActiveSeconds float64 `json:"active_seconds"`
	// Outcome counts, each a subset of Instructions.
	FirstTry    int64 `json:"first_try"`
	Reworked    int64 `json:"reworked"`
	Interrupted int64 `json:"interrupted"`
	Escalated   int64 `json:"escalated"`
	Rejected    int64 `json:"rejected"`
	// Tokens is what the turns used, input and output.
	Tokens int64 `json:"tokens"`
	// CostUSD is what was billed; APIEquivalentUSD what the same work
	// costs at list prices, its value where a plan covers it.
	CostUSD          float64 `json:"cost_usd"`
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	// UnpricedTurns counts turns on models with no list price, left out
	// of the money.
	UnpricedTurns int64 `json:"unpriced_turns"`
}

// Validate reports the first reason the upload may not cross the
// boundary, or nil.
func (u Upload) Validate() error {
	if u.Schema != SchemaVersion {
		return fmt.Errorf("schema %d: this server reads schema %d", u.Schema, SchemaVersion)
	}
	if !ValidUUID(u.BatchID) {
		return errors.New("batch_id: not a lower-case UUID")
	}
	if u.ComputedAt.IsZero() {
		return errors.New("computed_at: missing")
	}
	if u.ClientVersion != "" && !ValidLabel(u.ClientVersion) {
		return errors.New("client_version: not a version label")
	}
	if len(u.Days) == 0 {
		return errors.New("days: an upload covers at least one day")
	}
	if len(u.Days) > MaxDays {
		return fmt.Errorf("days: at most %d per upload", MaxDays)
	}
	if len(u.Buckets) > MaxBuckets {
		return fmt.Errorf("buckets: at most %d per upload", MaxBuckets)
	}
	covered := make(map[string]bool, len(u.Days))
	for _, d := range u.Days {
		if _, err := time.Parse(DayLayout, d); err != nil {
			return fmt.Errorf("days: %q is not a date", d)
		}
		covered[d] = true
	}
	type key struct{ day, repo, kind string }
	seen := make(map[key]bool, len(u.Buckets))
	for i, b := range u.Buckets {
		if err := b.Validate(); err != nil {
			return fmt.Errorf("buckets[%d]: %w", i, err)
		}
		if !covered[b.Day] {
			return fmt.Errorf("buckets[%d]: day %s is not in days", i, b.Day)
		}
		k := key{b.Day, b.Repo, b.Kind}
		if seen[k] {
			return fmt.Errorf("buckets[%d]: %s/%s/%s appears twice", i, b.Day, b.Repo, b.Kind)
		}
		seen[k] = true
	}
	return nil
}

// Validate reports the first reason the bucket is malformed, or nil.
func (b Bucket) Validate() error {
	if _, err := time.Parse(DayLayout, b.Day); err != nil {
		return fmt.Errorf("day %q is not a date", b.Day)
	}
	if !ValidRepo(b.Repo) {
		return errors.New("repo: not a repository label (name or owner/name)")
	}
	if !ValidKind(b.Kind) {
		return fmt.Errorf("kind %q: not a kind of work", b.Kind)
	}
	for name, v := range map[string]int64{
		"sessions": b.Sessions, "instructions": b.Instructions, "turns": b.Turns,
		"tool_calls": b.ToolCalls, "first_try": b.FirstTry, "reworked": b.Reworked,
		"interrupted": b.Interrupted, "escalated": b.Escalated, "rejected": b.Rejected,
		"tokens": b.Tokens, "unpriced_turns": b.UnpricedTurns,
	} {
		if v < 0 {
			return fmt.Errorf("%s: negative", name)
		}
	}
	for name, v := range map[string]int64{
		"first_try": b.FirstTry, "reworked": b.Reworked, "interrupted": b.Interrupted,
		"escalated": b.Escalated, "rejected": b.Rejected,
	} {
		if v > b.Instructions {
			return fmt.Errorf("%s: more than instructions", name)
		}
	}
	for name, v := range map[string]float64{
		"active_seconds": b.ActiveSeconds, "cost_usd": b.CostUSD, "api_equivalent_usd": b.APIEquivalentUSD,
	} {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%s: not a non-negative number", name)
		}
	}
	return nil
}

// EnrollRequest joins a machine to a team with an invite.
type EnrollRequest struct {
	Invite string `json:"invite"`
	// DisplayName is how the member appears to people granted to view
	// individual figures; ignored when the invite is for an existing
	// member.
	DisplayName string `json:"display_name"`
	// DeviceName tells the member's machines apart on their own page.
	DeviceName string `json:"device_name"`
}

// EnrollResponse is what a joined machine keeps. DeviceToken is shown once:
// the server keeps only its hash.
type EnrollResponse struct {
	OrgID       string `json:"org_id"`
	OrgName     string `json:"org_name"`
	TeamID      string `json:"team_id"`
	TeamName    string `json:"team_name"`
	MemberID    string `json:"member_id"`
	DeviceID    string `json:"device_id"`
	DeviceToken string `json:"device_token"`
}

// IngestResponse acknowledges an upload.
type IngestResponse struct {
	Accepted  int  `json:"accepted"`
	Duplicate bool `json:"duplicate,omitempty"`
	// Stale is set when a newer computation of these days is already held.
	Stale bool `json:"stale,omitempty"`
}

// Me is what the server holds about the member a token belongs to, and who
// can see it: the member's view of what is shared.
type Me struct {
	OrgName     string   `json:"org_name"`
	MemberID    string   `json:"member_id"`
	DisplayName string   `json:"display_name"`
	Role        string   `json:"role"`
	Teams       []string `json:"teams"`
	Devices     []Device `json:"devices"`
	// Buckets and Days are how many rows and days of figures are held.
	Buckets int `json:"buckets"`
	Days    int `json:"days"`
	// RetentionDays is how long figures are kept.
	RetentionDays int `json:"retention_days"`
	// MinGroupSize is the smallest group an aggregate is shown for.
	MinGroupSize int `json:"min_group_size"`
	// Viewers are the people granted to see this member's individual
	// figures.
	Viewers []Viewer `json:"viewers"`
	// Views are the recorded views of this member's individual figures.
	Views []View `json:"views"`
}

// Device is one of a member's enrolled machines.
type Device struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	EnrolledAt time.Time  `json:"enrolled_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// Viewer is a grant that covers the member.
type Viewer struct {
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Scope     string    `json:"scope"`
	Reason    string    `json:"reason"`
	GrantedAt time.Time `json:"granted_at"`
}

// View is one recorded view of the member's individual figures.
type View struct {
	Viewer string    `json:"viewer"`
	At     time.Time `json:"at"`
}

// LoginLink is a single-use link to the web view.
type LoginLink struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Error is the shape every failed request answers with, as the daemon API
// does.
type Error struct {
	Error string `json:"error"`
	Hint  string `json:"hint,omitempty"`
}
