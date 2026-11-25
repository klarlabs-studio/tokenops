// Package connections is the use case "which tools I use does TokenOps not
// read the plan of, and what do I do about it": the connection findings
// every surface shows, from one computation (ADR 0010). The daemon carries
// them on the glance, on the glance's cadence; the CLI computes them the
// same way when no daemon answers.
package connections

import (
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/observability/freshness"
	"go.klarlabs.de/tokenops/internal/contexts/spend/connection"
	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
	"go.klarlabs.de/tokenops/internal/contexts/spend/providers"
)

// The domain's types, named here so adapters do not reach past the
// capability (ADR 0010).
type (
	Connection = connection.Connection
	State      = connection.State
	Action     = connection.Action
	Evidence   = connection.Evidence
)

// The states.
const (
	Reading      = connection.Reading
	Connected    = connection.Connected
	Failing      = connection.Failing
	NotConnected = connection.NotConnected
	Dismissed    = connection.Dismissed
)

// Inputs is what one computation reads.
type Inputs struct {
	// Config is the configuration the sources run with and the
	// dismissals are read from; nil is none.
	Config *config.Config
	// Turns is each provider's turns over the last week (the glance's).
	Turns map[string]plans.ProviderTurns
	// Health is each reader's freshness; nil when unknown, as in a CLI
	// that reached no daemon.
	Health []freshness.Report
	// Reported are the providers a plan report reads now.
	Reported []string
	// Installed is, per provider, what of its tool is on this machine.
	Installed map[string][]string
	// SignIns is, per provider, whether another application's sign-in is
	// on this machine (sourceprobe.SignIns).
	SignIns map[string]bool
	Now     time.Time
}

// Compute is every provider's connection.
func Compute(in Inputs) []Connection {
	var cfg config.Config
	if in.Config != nil {
		cfg = *in.Config
	}
	use := make(map[string]connection.Use, len(in.Turns))
	for p, t := range in.Turns {
		use[p] = connection.Use{Turns: t.Turns, LastAt: t.LastAt}
	}
	reported := map[string]bool{}
	for _, p := range in.Reported {
		reported[p] = true
	}
	return connection.Classify(connection.Inputs{
		Now:       in.Now,
		Use:       use,
		Installed: in.Installed,
		SignIns:   in.SignIns,
		Sources:   sourcesOf(cfg, in.Health),
		Reported:  reported,
		Dismissed: cfg.DismissedProviders(),
	})
}

// Detected are the connections a surface lists as "also in use": in use or
// installed, no plan source on, not dismissed.
func Detected(cs []Connection) []Connection { return connection.Detected(cs) }

// sourcesOf is each plan source's state from cfg and the readers' health.
func sourcesOf(cfg config.Config, health []freshness.Report) []connection.Source {
	byTag := map[string]freshness.Report{}
	for _, r := range health {
		byTag[r.Tag] = r
	}
	switches := map[string]config.VendorUsageSource{}
	for _, s := range cfg.VendorUsageSources() {
		switches[s.SourceTag] = s
	}
	var out []connection.Source
	for _, s := range providers.Sources() {
		if !s.PlanSource() {
			continue
		}
		src := connection.Source{Tag: s.Tag, Switched: switched(cfg, s, switches[s.Tag])}
		if r, ok := byTag[s.Tag]; ok {
			src.Health, src.LastEventAt, src.LastError = string(r.State), r.LastEventAt, r.LastError
		}
		out = append(out, src)
	}
	return out
}

// switched reports whether the operator connected s, or it is on by
// default with nothing to give: an enabled config source, one on unless
// switched off, or an account source with a stored credential or a grant.
// An account reader that runs always but was given nothing is not, nor
// is a source that runs always (the status line): only its readings say
// it is connected.
func switched(cfg config.Config, s providers.SourceOf, sw config.VendorUsageSource) bool {
	switch s.Switch {
	case providers.SwitchConfig:
		return sw.Enabled || sw.AlwaysOn
	case providers.SwitchAccounts:
		if !sw.AlwaysOn {
			return false
		}
		id := string(s.Provider)
		_, stored := cfg.VendorUsage.Accounts.Credentials[id]
		_, granted := cfg.VendorUsage.Grants[id]
		return stored || (granted && (s.Credential == providers.AppLogin || len(s.AppLogins) > 0))
	}
	return false
}
