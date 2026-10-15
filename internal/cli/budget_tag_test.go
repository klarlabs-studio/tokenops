package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

// A budget can be scoped to an allocation tag, and the list says so.
func TestBudgetSetScopesToATag(t *testing.T) {
	path := seedConfig(t)
	if out, err := runBudgetSet(t, "--config", path, "acme", "--limit-usd", "50", "--tag", "client=Acme Corp"); err != nil {
		t.Fatalf("budget set: %v\n%s", err, out)
	}
	cfg, _ := config.ReadMutable(path)
	if len(cfg.Budgets) != 1 || cfg.Budgets[0].Tag != "client=Acme Corp" {
		t.Fatalf("budgets = %+v", cfg.Budgets)
	}
	if _, err := runBudgetSet(t, "--config", path, "bad", "--limit-usd", "5", "--tag", "team=x"); err == nil {
		t.Error("an unknown tag key was accepted")
	}
	cmd := withConfigFlag(newBudgetListCmd())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetArgs([]string{"--config", path})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "for client=Acme Corp") {
		t.Errorf("list = %q", out.String())
	}
}
