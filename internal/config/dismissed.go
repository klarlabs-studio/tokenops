package config

import (
	"slices"
	"sort"
)

// DismissProvider records whether the operator said not to read provider:
// on adds it to vendor_usage.dismissed, off takes it off. It reports
// whether the list changed.
func (c *Config) DismissProvider(provider string, on bool) bool {
	i := slices.Index(c.VendorUsage.Dismissed, provider)
	switch {
	case on && i < 0:
		c.VendorUsage.Dismissed = append(c.VendorUsage.Dismissed, provider)
		sort.Strings(c.VendorUsage.Dismissed)
		return true
	case !on && i >= 0:
		c.VendorUsage.Dismissed = slices.Delete(c.VendorUsage.Dismissed, i, i+1)
		if len(c.VendorUsage.Dismissed) == 0 {
			c.VendorUsage.Dismissed = nil
		}
		return true
	}
	return false
}

// DismissedProviders is vendor_usage.dismissed as a set.
func (c Config) DismissedProviders() map[string]bool {
	out := make(map[string]bool, len(c.VendorUsage.Dismissed))
	for _, p := range c.VendorUsage.Dismissed {
		out[p] = true
	}
	return out
}
