package cli

import (
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// `enable --admin-key` and the env var it names do different things: the
// flag path writes the secret into config.yaml, and so does the env path,
// because enable reads the variable and then persists what it found. An
// operator setting the variable to keep the key out of the file got the
// opposite of what they wanted, and the help did not say so.
//
// The daemon now reads the same variables at load time, which is the way
// to keep a credential off disk. The help has to distinguish them.
func TestEnableHelpSaysEnvVarsArePersisted(t *testing.T) {
	long := newVendorUsageEnableCmd().Long
	for _, want := range []string{"config.yaml", "does not keep"} {
		if !strings.Contains(long, want) {
			t.Errorf("enable help omits %q:\n%s", want, long)
		}
	}
}

// Every credential the daemon can take from the environment is named in
// the help, so the list cannot drift from config.CredentialEnvVars.
func TestEnableHelpNamesEveryCredentialEnvVar(t *testing.T) {
	long := newVendorUsageEnableCmd().Long
	for _, v := range config.CredentialEnvVars() {
		if v.Name == "TOKENOPS_DASHBOARD_ADMIN_TOKEN" {
			continue // not a vendor-usage source
		}
		if !strings.Contains(long, v.Name) {
			t.Errorf("enable help does not mention %s", v.Name)
		}
	}
}
