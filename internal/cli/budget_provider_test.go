package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// A budget can be scoped to one provider's bill; an unknown provider is
// refused with suggestions, and the list says whose bill each budget is.
func TestBudgetSetScopesToAProvider(t *testing.T) {
	path := seedConfig(t)
	if out, err := runBudgetSet(t, "--config", path, "mine", "--provider", "Fireworks", "--limit-usd", "50",
		"--window", "monthly", "--mode", "hard"); err != nil {
		t.Fatalf("budget set: %v\n%s", err, out)
	}
	cfg, _ := config.ReadMutable(path)
	if len(cfg.Budgets) != 1 || cfg.Budgets[0].Provider != "fireworks" {
		t.Fatalf("budgets = %+v", cfg.Budgets)
	}
	_, err := runBudgetSet(t, "--config", path, "typo", "--provider", "fireworx", "--limit-usd", "5")
	if err == nil || !strings.Contains(err.Error(), "did you mean fireworks") {
		t.Errorf("unknown provider: err = %v, want a suggestion", err)
	}
	if out, err := runBudgetSet(t, "--config", path, "both", "--provider", "fireworks", "--tag", "client=acme-test", "--limit-usd", "5"); err != nil {
		t.Fatalf("budget set with tag and provider: %v\n%s", err, out)
	}
	cmd := withConfigFlag(newBudgetListCmd())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--config", path})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"billed by fireworks", "for client=acme-test, billed by fireworks"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("list lacks %q:\n%s", want, out.String())
		}
	}
}
