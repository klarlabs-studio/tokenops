package accounts

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerQwenCloud registers the reader (readers_gen.go).
func readerQwenCloud() usage.Reader { return QwenCloud{} }

// QwenCloud reads the Qwen Cloud Individual Token Plan's 5-hour, weekly and
// monthly windows from the Qwen Cloud console (home.qwencloud.com) with its
// browser session, as CodexBar's Qwen Cloud provider does
// (Sources/CodexBarCore/Providers/QwenCloud/QwenCloudUsageFetcher.swift):
// the console's security token, then the Token Plan usage API through the
// cs-data gateway. CodexBar's Team credit-pool plugin is not ported.
type QwenCloud struct {
	// Home and Data replace home.qwencloud.com and cs-data.qwencloud.com
	// in tests.
	Home, Data string
	HTTP       *http.Client
}

func (QwenCloud) Endpoint() string               { return "qwencloud" }
func (QwenCloud) Provider() eventschema.Provider { return "qwencloud" }
func (QwenCloud) Source() string                 { return "qwencloud-web" }

func (q QwenCloud) Read(ctx context.Context, cookie string) (usage.Reading, error) {
	if !isSession(cookie) {
		return usage.Reading{}, fmt.Errorf("%w (Qwen Cloud is read with the console's Cookie header, not a key)", usage.ErrAuth)
	}
	home := base(q.Home, "https://home.qwencloud.com")
	c := tokenPlanConsole{
		origin: home, data: base(q.Data, "https://cs-data.qwencloud.com"),
		dashboard: home + "/billing/subscription/token-plan-individual",
		action:    "IntlBroadScopeAspnGateway", regionID: "ap-southeast-1",
	}
	token := consoleSecToken(ctx, q.HTTP, home, c.dashboard, cookie)
	if token == "" {
		return usage.Reading{}, fmt.Errorf("%w (no console security token on %s: the session is signed out)", usage.ErrAuth, strings.TrimPrefix(home, "https://"))
	}
	got, err := readTokenPlanWindows(ctx, q.HTTP, c, cookie, token)
	if errors.Is(err, errOtherRegion) {
		// The console accepted the session and has no Individual plan.
		return usage.Reading{Scope: "account"}, nil
	}
	return got, err
}
