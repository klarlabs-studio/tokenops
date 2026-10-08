package accounts

import (
	"os"
	"testing"

	"go.klarlabs.de/tokenops/internal/tools/gen"
)

// readers_gen.go lists every reader*() and gateway*() in this package; a
// reader added without regenerating it would never be polled.
func TestReaderListIsCurrent(t *testing.T) {
	want, err := gen.ListFile(".", "readers_gen.go", []gen.List{
		{Var: "readers", Prefix: "reader", Type: "usage.Reader"},
		{Var: "gateways", Prefix: "gateway", Type: "usage.Gateway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("readers_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("readers_gen.go is stale; run: go generate ./internal/infra/vendorusage/accounts")
	}
}
