package planswitch

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/contexts/spend/plans"
)

// The catalog lists each plan once, in name order, under the name config
// binds it by.
func TestCatalogIsEveryPlanByItsConfigName(t *testing.T) {
	got := Catalog()
	names := plans.Names()
	if len(got) != len(names) {
		t.Fatalf("%d plans, want %d", len(got), len(names))
	}
	for i, p := range got {
		if p.Name != names[i] {
			t.Errorf("plan %d is %q, want %q", i, p.Name, names[i])
		}
		if back, ok := Lookup(p.Name); !ok || back.Name != p.Name {
			t.Errorf("Lookup(%q) = %q, %v", p.Name, back.Name, ok)
		}
	}
}
