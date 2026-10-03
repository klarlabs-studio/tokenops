package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/infra/planhistory"
)

func scripted(answers ...string) (*wizard, *bytes.Buffer) {
	var out bytes.Buffer
	w := newWizard(strings.NewReader(strings.Join(answers, "\n")+"\n"), &out)
	return w, &out
}

// Only a terminal is asked; a pipe, a test, or --yes takes the defaults.
func TestInitAsksOnlyATerminal(t *testing.T) {
	if interactiveInput(strings.NewReader(""), false) {
		t.Error("a reader that is not a terminal was asked")
	}
	if interactiveInput(os.Stdin, true) {
		t.Error("--yes still asked")
	}
}

// Evidence that names two plans is put to the operator, and what they pay
// for the plan bound in this run is recorded in the plan history.
func TestWizardAsksWhatTheEvidenceLeftOpen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".tokenops"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"oauthAccount":{"organizationType":"claude_max"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("listen: 127.0.0.1:7878\nmoney:\n    currency: EUR\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	w, out := scripted("x", "2", "214,60")
	w.plans(cfgPath, home, nil)

	if !strings.Contains(out.String(), "Which plan are you on?") || !strings.Contains(out.String(), "pick 1-2") {
		t.Errorf("questions:\n%s", out)
	}
	cfg, err := config.ReadMutable(cfgPath)
	if err != nil || cfg.Plans["anthropic"] != "claude-max-20x" {
		t.Fatalf("plans %v, %v", cfg.Plans, err)
	}
	file, _ := planhistory.Default()
	h, err := file.Load()
	if err != nil {
		t.Fatal(err)
	}
	priced := false
	for _, b := range h {
		if b.Plan == "claude-max-20x" && b.Price == 214.6 && b.Currency == "EUR" {
			priced = true
		}
	}
	if !priced {
		t.Errorf("price not recorded: %+v", h)
	}
}

// A binding the operator made before init is not asked about again.
func TestWizardLeavesTheOperatorsPlansAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("listen: 127.0.0.1:7878\nplans:\n    openai: gpt-plus\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, out := scripted()
	w.plans(cfgPath, t.TempDir(), map[string]string{"openai": "gpt-plus"})
	if out.Len() != 0 {
		t.Errorf("asked about the operator's own plan:\n%s", out)
	}
}

func TestWizardPresetDefaultsToAdvise(t *testing.T) {
	w, _ := scripted("")
	if got := w.preset(); got != "advise" {
		t.Errorf("Enter chose %q", got)
	}
	w, _ = scripted("4")
	if got := w.preset(); got != "autopilot" {
		t.Errorf("4 chose %q", got)
	}
}

func TestWizardOffersTheDaemonOnlyWhenUnsupervised(t *testing.T) {
	unsupervised := []setupStep{{Name: "daemon unit", Manual: true}}
	installed := 0
	w, _ := scripted("")
	w.installDaemon = func(io.Writer) error { installed++; return nil }
	w.daemon(unsupervised)
	if installed != 1 {
		t.Errorf("Enter did not install (%d)", installed)
	}
	w, _ = scripted("n")
	w.installDaemon = func(io.Writer) error { installed++; return nil }
	w.daemon(unsupervised)
	w.daemon([]setupStep{{Name: "daemon unit", Detail: "already installed"}})
	if installed != 1 {
		t.Errorf("installed %d times, want once", installed)
	}
	w, out := scripted("y")
	w.installDaemon = func(io.Writer) error { return errors.New("launchctl refused") }
	w.daemon(unsupervised)
	if !strings.Contains(out.String(), "tokenops daemon install") {
		t.Errorf("a failed install says nothing useful:\n%s", out)
	}
}

// Running out of answers is not consent: nothing is installed, and nothing
// more is asked.
func TestWizardEndOfInputIsNotYes(t *testing.T) {
	var out bytes.Buffer
	w := newWizard(strings.NewReader(""), &out)
	installed := false
	w.installDaemon = func(io.Writer) error { installed = true; return nil }
	w.daemon([]setupStep{{Name: "daemon unit", Manual: true}})
	if installed {
		t.Fatal("end of input installed the daemon")
	}
	if got := w.preset(); got != "advise" {
		t.Errorf("preset after end of input = %q, want the default", got)
	}
	if strings.Contains(out.String(), "How much should the coach do?") {
		t.Errorf("asked again after input ended:\n%s", out.String())
	}
	if w.choose("q", []string{"a", "b"}, 0, true) != -1 || w.amount("q") != 0 {
		t.Error("a skippable question after end of input did not skip")
	}
}
