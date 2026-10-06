# tokenops

What your coding agents cost, where they leak, and every plan's limits —
read on your machine. Your prompts and your code never leave it.

```bash
npx tokenops checkup
```

reads the last week of Claude Code, Codex, Gemini CLI and opencode work,
with no setup: tokens and their value at API prices per harness and model,
how the sessions went, and each leak with the one command that fixes it.

This package is a launcher for the prebuilt `tokenops` binary for your
platform (macOS and Linux, x64 and arm64), installed as an optional
dependency. For the daemon, the coach and the menu bar app, install it
with Homebrew:

```bash
brew install --cask klarlabs-studio/tap/tokenops
tokenops init
```

Docs: https://github.com/klarlabs-studio/tokenops · Apache-2.0
