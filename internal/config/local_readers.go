package config

// LocalReader is the switch every local transcript reader shares: the
// readers that only read files a client wrote on this machine, as opposed
// to pollers that call a vendor with a login.
//
// Enabled alone cannot say what the operator chose. Every config an older
// `tokenops init` wrote carries `enabled: false` for each reader it did not
// turn on, so that value means "never set" as often as "off". SwitchedOff
// is written only by `tokenops vendor-usage enable <source> --disable`, so
// it is the one record of an explicit "off" (ADR 0009, autonomous by
// default: TokenOps turns a reader on when its data appears, unless the
// operator switched it off).
type LocalReader struct {
	Enabled     *bool
	SwitchedOff *bool
}

// localReaders maps each local reader's source name to its switch.
func (c *Config) localReaders() map[string]LocalReader {
	v := &c.VendorUsage
	return map[string]LocalReader{
		"claude-code-jsonl": {&v.ClaudeCodeJSONL.Enabled, &v.ClaudeCodeJSONL.SwitchedOff},
		"codex-jsonl":       {&v.CodexJSONL.Enabled, &v.CodexJSONL.SwitchedOff},
		"opencode":          {&v.OpenCode.Enabled, &v.OpenCode.SwitchedOff},
		"gemini-cli":        {&v.GeminiCLI.Enabled, &v.GeminiCLI.SwitchedOff},
		"pi-sessions":       {&v.Pi.Enabled, &v.Pi.SwitchedOff},
	}
}

// SetLocalReader records the operator switching a local reader on or off.
// Off is recorded as switched off, so nothing turns the reader back on by
// itself. It reports false for a source that is not a local reader.
func (c *Config) SetLocalReader(source string, on bool) bool {
	r, ok := c.localReaders()[source]
	if !ok {
		return false
	}
	*r.Enabled = on
	*r.SwitchedOff = !on
	return true
}

// TurnOnLocalReaders turns on each reader in present (the readers whose
// data is on this machine) that is off and was not switched off by the
// operator. It returns the readers it turned on, in present's order, so
// the caller can say so once: a second call with the same data turns on
// nothing.
func (c *Config) TurnOnLocalReaders(present []string) []string {
	readers := c.localReaders()
	var out []string
	for _, src := range present {
		r, ok := readers[src]
		if !ok || *r.Enabled || *r.SwitchedOff {
			continue
		}
		*r.Enabled = true
		out = append(out, src)
	}
	return out
}

// EnabledLocalReaders is the set of local readers switched on, by source
// name. The daemon compares the file's set with the one it started with to
// know a reader was turned on after it read its config.
func (c Config) EnabledLocalReaders() map[string]bool {
	out := map[string]bool{}
	for name, r := range c.localReaders() {
		if *r.Enabled {
			out[name] = true
		}
	}
	return out
}

// LocalReaderSwitchedOff reports whether the operator switched source off.
func (c Config) LocalReaderSwitchedOff(source string) bool {
	r, ok := c.localReaders()[source]
	return ok && *r.SwitchedOff
}
