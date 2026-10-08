package accounts

import (
	"context"
	"errors"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

const xaiTeam = "65c1e471-205f-4566-9c5a-07198bcdf4ce"

// The ledger is inverted cents: "-1000" is $10 of credit left. Only the
// management key is sent, as a bearer; the team is in the path.
func TestXAIReadsThePrepaidBalance(t *testing.T) {
	srv := serve(t, "/v1/billing/teams/"+xaiTeam+"/prepaid/balance", "xai-mgmt", fixture(t, "xai"))
	defer srv.Close()
	got, err := XAI{BaseURL: srv.URL}.Read(context.Background(), xaiTeam+":xai-mgmt")
	if err != nil || !got.HasBalance || !approx(got.BalanceUSD, 10) || got.Scope != "team" || got.HasUsed {
		t.Errorf("%+v %v", got, err)
	}
}

// A refused key, a team the key does not belong to, and a credential that
// is not TEAM_ID:MANAGEMENT_KEY (an inference key alone) are all refused.
func TestXAIRefusals(t *testing.T) {
	srv := serve(t, "/v1/billing/teams/"+xaiTeam+"/prepaid/balance", "xai-mgmt", fixture(t, "xai"))
	defer srv.Close()
	for _, cred := range []string{xaiTeam + ":bad", "other-team:xai-mgmt", "xai-inference-key", ":xai-mgmt", "../x:xai-mgmt"} {
		if _, err := (XAI{BaseURL: srv.URL}).Read(context.Background(), cred); !errors.Is(err, usage.ErrAuth) {
			t.Errorf("%q: err %v", cred, err)
		}
	}
}

func TestXAIUnknownShape(t *testing.T) {
	for _, body := range []string{`{}`, `{"total":{"val":"ten"}}`} {
		srv := serve(t, "/v1/billing/teams/t/prepaid/balance", "k", body)
		got, err := XAI{BaseURL: srv.URL}.Read(context.Background(), "t:k")
		srv.Close()
		if err == nil {
			t.Errorf("%s: read %+v", body, got)
		}
	}
}
