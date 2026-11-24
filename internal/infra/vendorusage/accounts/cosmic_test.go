package accounts

import (
	"context"
	"errors"
	"testing"

	usage "go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/accounts"
)

func TestCosmicInputAndOutputAllowances(t *testing.T) {
	srv := serveHeaders(t, "/v3/projects/usage", map[string]string{"Authorization": "Bearer pat", "Origin": "https://app.cosmicjs.com"}, fixture(t, "cosmic"))
	defer srv.Close()
	got, err := Cosmic{BaseURL: srv.URL}.Read(context.Background(), "proj_1:pat")
	if err != nil || len(got.Windows) != 2 || got.Windows[0].Name != "tokens (input)" || !approx(got.Windows[0].UsedPct, 25) ||
		got.Windows[1].Name != "tokens (output)" || !approx(got.Windows[1].UsedPct, 10) {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := (Cosmic{}).Read(context.Background(), "pat"); !errors.Is(err, usage.ErrAuth) {
		t.Errorf("no project: %v", err)
	}
}

func TestCosmicShapes(t *testing.T) {
	for _, tc := range []struct {
		body            string
		ok              bool
		windows, counts int
	}{
		{`{"usage":{"ai":{"input_tokens":10,"output_tokens":5}},"plan_info":{"ai_tokens":{"max_input":"unlimited","max_output":100}}}`, true, 1, 1},
		{`{"usage":{"ai":{"input_tokens":10}}}`, true, 0, 1},
		{`{"usage":{"ai":{}}}`, false, 0, 0},
		{`{"usage":{"ai":{"input_tokens":-1}}}`, false, 0, 0},
		{`{}`, false, 0, 0},
	} {
		srv := serveHeaders(t, "/v3/projects/usage", map[string]string{"Authorization": "Bearer pat"}, tc.body)
		got, err := Cosmic{BaseURL: srv.URL}.Read(context.Background(), "proj_1:pat")
		srv.Close()
		if (err == nil) != tc.ok || (tc.ok && (len(got.Windows) != tc.windows || len(got.Counts) != tc.counts)) {
			t.Errorf("%s: %+v %v", tc.body, got, err)
		}
	}
}
