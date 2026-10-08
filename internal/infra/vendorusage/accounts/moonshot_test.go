package accounts

import (
	"context"
	"testing"
)

func TestMoonshotEmptyBalanceIsReached(t *testing.T) {
	srv := serve(t, "/v1/users/me/balance", "sk-ms", fixture(t, "moonshot"))
	defer srv.Close()
	got, err := Moonshot{BaseURL: srv.URL}.Read(context.Background(), "sk-ms")
	if err != nil || !got.HasBalance || !got.LimitReached {
		t.Errorf("%+v %v", got, err)
	}
}
