package cli

import (
	"strings"

	"go.klarlabs.de/tokenops/internal/capability/connections"
)

// notConnectedLines lists, under `tokenops status`, each tool in use whose
// plan is not read: what was seen, the command, what it needs and where
// to find it, and how to stop being told. Nothing when there is none.
func notConnectedLines(cs []connections.Connection) []string {
	if len(cs) == 0 {
		return nil
	}
	lines := []string{"not connected (in use here, plan not read):"}
	for _, c := range cs {
		seen := make([]string, 0, len(c.Evidence))
		for _, e := range c.Evidence {
			seen = append(seen, e.Detail)
		}
		lines = append(lines, "  "+c.Display+" — "+strings.Join(seen, "; "))
		if a := c.Action; a != nil {
			lines = append(lines, "    → "+a.Command)
			needs := "      needs " + a.Needs
			if a.Where != "" {
				needs += " — " + a.Where
			}
			lines = append(lines, needs, "      docs "+a.Docs, "      not wanted: "+a.Dismiss)
		}
	}
	return lines
}

// alsoInUse is the glance's one-line footer naming the tools in use whose
// plan is not read, each with its command; empty when there is none.
func alsoInUse(cs []connections.Connection) string {
	detected := connections.Detected(cs)
	if len(detected) == 0 {
		return ""
	}
	parts := make([]string, 0, len(detected))
	for _, c := range detected {
		part := c.Display + " (not connected"
		if c.Action != nil {
			part += " — " + c.Action.Command
		}
		parts = append(parts, part+")")
	}
	return "Also in use: " + strings.Join(parts, ", ") + "\n"
}
