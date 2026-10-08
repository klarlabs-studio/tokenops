package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerMuse registers the reader (readers_gen.go).
func readerMuse() usage.Reader { return Muse{} }

// Muse reads Muse Code's (Meta's coding CLI) subscription windows with the
// CLI's own sign-in, granted by the operator (ADR 0013), as CodexBar's Muse
// provider does: POST /muse-code/key on api.meta.ai answers the
// subscription's 5-hour and weekly windows. The same answer carries an
// inference key; it is not decoded, kept or logged.
type Muse struct {
	BaseURL string
	HTTP    *http.Client
}

func (Muse) Endpoint() string               { return "muse" }
func (Muse) Provider() eventschema.Provider { return "muse" }
func (Muse) Source() string                 { return "muse-account" }

// museWindow is one subscription window, in Muse's own units.
type museWindow struct {
	UsedPercent        number `json:"used_percent"`
	WindowDurationMins number `json:"window_duration_mins"`
	ResetsAt           stamp  `json:"resets_at"`
}

func (m Muse) Read(ctx context.Context, token string) (usage.Reading, error) {
	// The CLI's tokens are "dca:"-prefixed; anything else is not its sign-in.
	if !strings.HasPrefix(token, "dca:") {
		return usage.Reading{}, fmt.Errorf("%w (not a Muse Code sign-in)", usage.ErrAuth)
	}
	var resp struct {
		IsSubsActive *bool `json:"is_subs_active"`
		SubsUsage    *struct {
			Window *museWindow `json:"window"`
			Weekly *museWindow `json:"weekly"`
		} `json:"subs_usage"`
	}
	header := http.Header{"Authorization": {"Bearer " + token}, "x-api-version": {"1.0.0"}}
	if err := postJSON(ctx, m.HTTP, base(m.BaseURL, "https://api.meta.ai")+"/muse-code/key", header, struct{}{}, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp.IsSubsActive == nil {
		return usage.Reading{}, errors.New("accounts: POST api.meta.ai/muse-code/key: unexpected answer")
	}
	r := usage.Reading{Scope: "account"}
	if !*resp.IsSubsActive || resp.SubsUsage == nil {
		// No subscription, or its 5-hour window idle: nothing to show.
		return r, nil
	}
	if w := resp.SubsUsage.Window; w != nil && w.UsedPercent.ok {
		d := 5 * time.Hour
		if w.WindowDurationMins.ok && w.WindowDurationMins.v > 0 {
			d = time.Duration(w.WindowDurationMins.v) * time.Minute
		}
		r.Windows = append(r.Windows, usage.Window{Name: windowName(d), UsedPct: clampPct(w.UsedPercent.v), Duration: d, ResetsAt: w.ResetsAt.t})
	}
	if w := resp.SubsUsage.Weekly; w != nil && w.UsedPercent.ok {
		d := 7 * 24 * time.Hour
		r.Windows = append(r.Windows, usage.Window{Name: windowName(d), UsedPct: clampPct(w.UsedPercent.v), Duration: d, ResetsAt: w.ResetsAt.t})
	}
	r.Subscription = len(r.Windows) > 0
	return r, nil
}
