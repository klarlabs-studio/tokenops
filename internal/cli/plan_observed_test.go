package cli

import (
	"strings"
	"testing"
)

// plan observed says there is nothing observed, reset turns the overlay
// off and says so, and on turns it back on; none needs a store.
func TestPlanObserved(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(k, home)
	}
	steps := []struct {
		args []string
		want string
	}{
		{[]string{"plan", "observed"}, "No observed limits"},
		{[]string{"plan", "observed", "reset"}, "cleared and off"},
		{[]string{"plan", "observed"}, "are off"},
		{[]string{"plan", "observed", "--json"}, `"off": true`},
		{[]string{"plan", "observed", "on"}, "No observed limits"},
		{[]string{"plan", "observed", "--json"}, `"off": false`},
	}
	for _, s := range steps {
		out, err := runCLI(t, nil, s.args...)
		if err != nil || !strings.Contains(out, s.want) {
			t.Fatalf("%v = %q, %v; want %q", s.args, out, err, s.want)
		}
	}
}
