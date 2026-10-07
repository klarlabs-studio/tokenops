package usagemeter

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
	"go.klarlabs.de/tokenops/internal/contexts/spend/vendorusage/claudeusagemeter"
)

func TestApplyKeepsABrowserSessionReadFromTheBrowser(t *testing.T) {
	conn := Connection{Org: claudeusagemeter.OrgEntry{UUID: "work"}}
	var cfg config.Config
	Apply(&cfg, Session{Key: "k", Clearance: "cf", UserAgent: "ua", Browser: "Chrome"}, conn)
	m := cfg.VendorUsage.ClaudeUsageMeter
	if !m.Enabled || !m.FromBrowser || m.Browser != "Chrome" || m.Clearance != "cf" || m.UserAgent != "ua" || m.OrgID != "work" {
		t.Errorf("browser session applied as %+v", m)
	}

	// A pasted key reads nothing from a browser, and replaces one that did.
	Apply(&cfg, Session{Key: "pasted"}, conn)
	m = cfg.VendorUsage.ClaudeUsageMeter
	if m.FromBrowser || m.SessionKey != "pasted" || m.Clearance != "" {
		t.Errorf("pasted key applied as %+v", m)
	}
}
