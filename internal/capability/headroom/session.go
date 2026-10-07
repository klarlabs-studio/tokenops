package headroom

import (
	"go.klarlabs.de/tokenops/internal/contexts/spend/session"
	"go.klarlabs.de/tokenops/internal/events"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// SessionTracker records an MCP session's tool calls as plan activity, so
// session budgets see a client that never goes through the proxy.
type SessionTracker = session.Tracker

// SessionPing is what one tracked tool call is recorded as.
type SessionPing = session.Options

// NewSessionTracker publishes provider's session pings to bus.
func NewSessionTracker(bus events.Bus, provider eventschema.Provider) *SessionTracker {
	return session.New(bus, session.Options{Provider: provider})
}
