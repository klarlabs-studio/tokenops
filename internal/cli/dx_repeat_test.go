package cli

import (
	"bytes"
	"strings"
	"testing"

	"go.klarlabs.de/tokenops/internal/capability/sessions"
)

// The repeated-command line shows only when read-only commands ran.
func TestWriteDXTextShowsRepeatedCommands(t *testing.T) {
	var b bytes.Buffer
	dx := sessions.DX{Window: "7d"}
	dx.Metrics.Prompts = 3
	dx.Metrics.ReadOnlyCommands, dx.Metrics.RepeatedCommands, dx.Metrics.RepeatedCommandRatePct = 40, 9, 22.5
	writeDXText(&b, dx, nil, 7)
	if !strings.Contains(b.String(), "repeated commands:     22.5%") || !strings.Contains(b.String(), "9 of 40 read-only commands") {
		t.Errorf("output:\n%s", b.String())
	}
	b.Reset()
	dx.Metrics.ReadOnlyCommands = 0
	writeDXText(&b, dx, nil, 7)
	if strings.Contains(b.String(), "repeated commands") {
		t.Errorf("shown with no read-only commands:\n%s", b.String())
	}
}
