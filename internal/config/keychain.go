package config

// KeychainConfig is the operator's switch for the macOS Keychain.
//
// TokenOps reads two kinds of Keychain item: a browser's "Safe Storage" key,
// to read claude.ai's session cookies, and Claude Code's own sign-in. Every
// unattended read is quiet, never a prompt; Disabled turns all of them off,
// prompting setup reads included. A browser that keeps its cookies outside
// the Keychain (Firefox) and the credentials file still work.
type KeychainConfig struct {
	Disabled bool `yaml:"disabled,omitempty"`
}
