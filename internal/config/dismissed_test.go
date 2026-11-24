package config

import (
	"reflect"
	"testing"
)

func TestDismissProvider(t *testing.T) {
	var c Config
	if !c.DismissProvider("cursor", true) || !c.DismissProvider("amp", true) || c.DismissProvider("cursor", true) {
		t.Fatal("adding: changed once per provider")
	}
	if want := []string{"amp", "cursor"}; !reflect.DeepEqual(c.VendorUsage.Dismissed, want) {
		t.Fatalf("dismissed = %v, want %v", c.VendorUsage.Dismissed, want)
	}
	if !c.DismissedProviders()["cursor"] {
		t.Error("cursor not in the set")
	}
	if !c.DismissProvider("cursor", false) || c.DismissProvider("cursor", false) || !c.DismissProvider("amp", false) {
		t.Fatal("removing: changed once per provider")
	}
	if c.VendorUsage.Dismissed != nil {
		t.Errorf("dismissed = %v, want nil so nothing is written", c.VendorUsage.Dismissed)
	}
}
