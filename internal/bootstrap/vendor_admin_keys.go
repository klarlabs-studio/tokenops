package bootstrap

import (
	"context"
	"strings"
	"time"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/pkg/eventschema"
)

// openAIAdminEndpoint is the OpenAI admin reader's endpoint, the only one
// an admin key found for OpenAI is sent to.
const openAIAdminEndpoint = "openai-admin"

// AdminKeys are the organisation admin keys configured on this machine,
// by provider: Anthropic's from vendor_usage.anthropic.admin_key (or
// TOKENOPS_ANTHROPIC_ADMIN_KEY), OpenAI's as the openai-admin reader
// finds it (`tokenops vendor-usage setup openai`, OPENAI_ADMIN_KEY). The
// team upload reads the vendors' cost with them on an owner's or admin's
// machine (ADR 0012 §11); the keys never leave it.
func AdminKeys(ctx context.Context, cfg config.Config) map[string]string {
	keys := map[string]string{}
	if k := strings.TrimSpace(cfg.VendorUsage.Anthropic.AdminKey); k != "" {
		keys[string(eventschema.ProviderAnthropic)] = k
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, c := range vendorAccountCredentials(cfg)() {
		if c.Endpoint != openAIAdminEndpoint {
			continue
		}
		key := strings.TrimSpace(c.Key)
		if key == "" && c.Resolve != nil {
			key, _ = c.Resolve(ctx)
		}
		if key = strings.TrimSpace(key); key != "" {
			keys[string(eventschema.ProviderOpenAI)] = key
			break
		}
	}
	return keys
}
