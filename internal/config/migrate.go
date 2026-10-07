package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// renamedKeys maps a key that no longer exists to what replaced it.
//
// A renamed YAML key is silently ignored by the decoder, so a config that
// still uses the old spelling loads cleanly and the feature simply never
// runs — the exact shape of failure this project keeps removing. These are
// refused loudly instead, naming the replacement, because the error message
// is the only migration aid a clean break offers.
var renamedKeys = map[string]string{
	"anthropic_cookie": "claude_usage_meter",
}

// checkRenamedKeys refuses a config still using a retired key.
func checkRenamedKeys(data []byte, path string) error {
	var raw struct {
		VendorUsage map[string]any `yaml:"vendor_usage"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil // the real decode already reported anything fatal
	}
	for old, replacement := range renamedKeys {
		if _, present := raw.VendorUsage[old]; !present {
			continue
		}
		return fmt.Errorf(
			"config %q: vendor_usage.%s was renamed to vendor_usage.%s.\n"+
				"  The old name described how the data was fetched rather than what it gives you.\n"+
				"  Rename the key (its contents are unchanged), or re-run "+
				"`tokenops vendor-usage setup %s`.\n"+
				"  Events already stored under the old source tag keep it and are not counted "+
				"against the new name",
			path, old, replacement, "claude-subscription")
	}
	return nil
}
