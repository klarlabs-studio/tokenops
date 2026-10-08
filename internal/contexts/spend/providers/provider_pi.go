package providers

func providerPi() Descriptor {
	return Descriptor{
		ID:          "pi",
		DisplayName: "Pi",
		Sources: []Source{
			// Pi is a harness, not a vendor: its turns are billed by the
			// backend each called (Anthropic, OpenAI's Codex sign-in, ...),
			// so the source reports on those providers, as opencode's does.
			{Name: "pi_sessions", Tag: "pi-sessions", Kind: LocalLog, Credential: LocalFile,
				Switch: SwitchConfig, Reader: BespokeReader, Verified: FromCodexBar, AnyProvider: true,
				Reference: "CodexBar Sources/CodexBarCore/PiSessionCostScanner.swift (docs/pi.md)",
				Package:   "internal/contexts/spend/vendorusage/pisessions",
				Fixture:   "internal/contexts/spend/vendorusage/pisessions/reader_test.go",
				Shows:     "per-turn tokens per provider and model from Pi's and OMP's session transcripts"},
		},
		Docs: Docs{Label: "Pi coding agent", Setup: "`tokenops vendor-usage enable pi-sessions` (turned on by `tokenops init` when Pi's sessions are on this machine)"},
	}
}
