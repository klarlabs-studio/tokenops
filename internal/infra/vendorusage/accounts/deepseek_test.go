package accounts

import (
	"context"
	"testing"
)

func TestDeepSeekUsesTheUSDBalance(t *testing.T) {
	srv := serve(t, "/user/balance", "sk-ds", fixture(t, "deepseek"))
	defer srv.Close()
	got, err := DeepSeek{BaseURL: srv.URL}.Read(context.Background(), "sk-ds")
	if err != nil || !got.HasBalance || got.BalanceUSD != 7.25 || got.LimitReached || got.HasUsed {
		t.Errorf("%+v %v", got, err)
	}
}
