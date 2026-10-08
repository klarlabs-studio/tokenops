package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// readerMuse registers the reader (readers_gen.go).
func readerMuse() usage.Reader { return Muse{} }

// Muse reads Muse Code's 5-hour and weekly quota with the device token
// `muse login` issues ("dca:…"), from the call Muse Code's CLI makes at
// start-up, POST api.meta.ai/muse-code/key, as CodexBar's Muse provider
// does (Sources/CodexBarCore/Providers/Muse/MuseCredentials.swift,
// Sources/CodexBarCore/Resources/Plugins/muse.ts). The answer also
// carries an inference key and the payment method; neither is kept.
// Meta leaves the quota out while the 5-hour window is idle: such an
// answer is an empty reading.
type Muse struct {
	BaseURL string
	HTTP    *http.Client
}

func (Muse) Endpoint() string               { return "muse" }
func (Muse) Provider() eventschema.Provider { return "muse" }
func (Muse) Source() string                 { return "muse-account" }

// museMaxReset is CodexBar's bound on a reset time (year 4000).
const museMaxReset = 64092211200

func (m Muse) Read(ctx context.Context, token string) (usage.Reading, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "dca:") || strings.ContainsAny(token, " \r\n") {
		// Dashboard (LLM_…) and inference (LLM|…) keys are never sent.
		return usage.Reading{}, fmt.Errorf("%w (Muse Code is read with the dca: device token from `muse login`)", usage.ErrAuth)
	}
	header := http.Header{"Authorization": {"Bearer " + token}, "X-Api-Version": {"1.0.0"}, "User-Agent": {"TokenOps"}}
	var resp map[string]json.RawMessage
	if err := postJSON(ctx, m.HTTP, base(m.BaseURL, "https://api.meta.ai")+"/muse-code/key", header, map[string]any{}, &resp); err != nil {
		return usage.Reading{}, err
	}
	if resp == nil {
		return usage.Reading{}, errors.New("accounts: POST api.meta.ai/muse-code/key: unexpected shape")
	}
	requirePayment, ok1 := museBool(resp["require_payment"])
	active, ok2 := museBool(resp["is_subs_active"])
	if !ok1 || !ok2 {
		return usage.Reading{}, errors.New("accounts: POST api.meta.ai/muse-code/key: unexpected shape")
	}
	if requirePayment || !active {
		// No subscription to read: nothing to store.
		return usage.Reading{Scope: "account"}, nil
	}
	raw, ok := resp["subs_usage"]
	if !ok || string(raw) == "null" {
		return usage.Reading{Scope: "account"}, nil
	}
	var su struct {
		Window *struct {
			UsedPct  *float64        `json:"used_percent"`
			Duration json.RawMessage `json:"window_duration_mins"`
			ResetsAt json.RawMessage `json:"resets_at"`
		} `json:"window"`
		Weekly *struct {
			UsedPct  *float64        `json:"used_percent"`
			ResetsAt json.RawMessage `json:"resets_at"`
		} `json:"weekly"`
	}
	if err := json.Unmarshal(raw, &su); err != nil || su.Window == nil || su.Weekly == nil || su.Window.UsedPct == nil || su.Weekly.UsedPct == nil {
		return usage.Reading{}, errors.New("accounts: POST api.meta.ai/muse-code/key: unexpected quota shape")
	}
	var mins float64
	if json.Unmarshal(su.Window.Duration, &mins) != nil || mins <= 0 || mins > 1e9 || math.IsNaN(mins) {
		return usage.Reading{}, errors.New("accounts: POST api.meta.ai/muse-code/key: unexpected window length")
	}
	d := time.Duration(math.Round(mins)) * time.Minute
	short, err := museReset(su.Window.ResetsAt)
	if err != nil {
		return usage.Reading{}, err
	}
	week, err := museReset(su.Weekly.ResetsAt)
	if err != nil {
		return usage.Reading{}, err
	}
	return usage.Reading{Scope: "account", Subscription: true, Windows: []usage.Window{
		{Name: windowName(d), UsedPct: clampPct(*su.Window.UsedPct), Duration: d, ResetsAt: short},
		{Name: "week", UsedPct: clampPct(*su.Weekly.UsedPct), Duration: 7 * 24 * time.Hour, ResetsAt: week},
	}}, nil
}

// museBool reads an optional boolean: absent and null are false, any
// other type is not a Muse answer.
func museBool(raw json.RawMessage) (bool, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return false, true
	}
	var b bool
	if json.Unmarshal(raw, &b) != nil {
		return false, false
	}
	return b, true
}

// museReset reads a reset in Unix seconds; absent, null, or out of range
// is no reset, any other type is an unexpected answer.
func museReset(raw json.RawMessage) (time.Time, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, nil
	}
	var v float64
	if json.Unmarshal(raw, &v) != nil {
		return time.Time{}, errors.New("accounts: POST api.meta.ai/muse-code/key: unexpected reset time")
	}
	if v <= 0 || v > museMaxReset {
		return time.Time{}, nil
	}
	return time.Unix(int64(v), 0).UTC(), nil
}
