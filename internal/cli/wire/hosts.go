package wire

import (
	"os"
	"path/filepath"
	"runtime"
)

// Host is an MCP client whose configuration tokenops can register itself in.
type Host struct {
	// Name is what the operator calls this client.
	Name string
	// ConfigPath is the file holding its mcpServers map.
	ConfigPath string
}

// DiscoverHosts returns the MCP hosts actually present under home.
//
// Only existing config files are returned. Creating one for a client the
// operator does not have would leave a dead entry behind and make the
// summary claim more than was done.
func DiscoverHosts(home string) []Host {
	candidates := []Host{
		{Name: "Claude Code", ConfigPath: filepath.Join(home, ".claude.json")},
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, Host{
			Name: "Claude Desktop",
			ConfigPath: filepath.Join(home, "Library", "Application Support",
				"Claude", "claude_desktop_config.json"),
		})
	} else {
		candidates = append(candidates, Host{
			Name:       "Claude Desktop",
			ConfigPath: filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"),
		})
	}

	out := make([]Host, 0, len(candidates))
	for _, h := range candidates {
		if _, err := os.Stat(h.ConfigPath); err == nil {
			out = append(out, h)
		}
	}
	return out
}
