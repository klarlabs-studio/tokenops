package claudeusagemeter

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Connection is a session key Anthropic accepted, and the organization it
// will meter.
type Connection struct {
	Org   OrgEntry
	Usage *UsageResponse
	// Orgs is every organization the key belongs to, for a caller that
	// wants to offer a choice.
	Orgs []OrgEntry
}

// ErrNothingToMeter means the key works but no organization it belongs to
// reports usage limits.
var ErrNothingToMeter = errors.New("claude-usage-meter: Anthropic reports no usage limits for this account")

// Connect verifies the client's key with Anthropic and picks the
// organization to meter: the one named by choose (UUID or name), else the
// first that reports usage. An account can hold an Enterprise org beside a
// personal or API one, and only some carry usage; an organization whose
// usage call fails (an API org answers 403) is passed over.
func Connect(ctx context.Context, c *Client, choose string) (Connection, error) {
	orgs, err := c.Organizations(ctx)
	if err != nil {
		return Connection{}, err
	}
	if len(orgs) == 0 {
		return Connection{}, errors.New("claude-usage-meter: the key worked but the account has no organizations")
	}
	if choose = strings.TrimSpace(choose); choose != "" {
		for _, o := range orgs {
			if o.UUID == choose || strings.EqualFold(o.Name, choose) {
				u, err := c.Usage(ctx, o.UUID)
				if err != nil {
					return Connection{}, err
				}
				if !u.HasSignal() {
					return Connection{}, fmt.Errorf("%w (organization %q)", ErrNothingToMeter, o.Name)
				}
				return Connection{Org: o, Usage: u, Orgs: orgs}, nil
			}
		}
		return Connection{}, fmt.Errorf("claude-usage-meter: no organization %q on this account", choose)
	}
	for _, o := range orgs {
		if u, err := c.Usage(ctx, o.UUID); err == nil && u.HasSignal() {
			return Connection{Org: o, Usage: u, Orgs: orgs}, nil
		}
	}
	return Connection{}, ErrNothingToMeter
}
