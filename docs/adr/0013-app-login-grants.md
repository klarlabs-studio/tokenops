# ADR 0013 — Other applications' sign-ins, read only by grant

- **Status:** Accepted 2026-10-08.
- **Date:** 2026-10-08
- **Deciders:** TokenOps maintainers (the repository owner decided the
  question this ADR answers on 2026-10-08)
- **Related:** ADR 0011 §1.4 (a credential owned by another application),
  `docs/providers.md`, `internal/infra/applogin`,
  `internal/infra/browserstorage`

## Context

ADR 0011 put "a credential owned by another application" last among a
provider's sources, opt-in only, and shipped one: Claude Code's sign-in
(`setup claude-code`). Porting CodexBar's providers met many more. A dozen
vendors' usage can be read with nothing but the sign-in their own CLI or app
keeps: the kilo CLI's `auth.json`, the codebuff CLI's credentials (the only
credential that reads Codebuff's weekly limit), Hermes Agent's Nous Portal
token, Hugging Face's `hf auth login` token, kiro-cli's database, Antigravity's
language server token on its command line. Each provider descriptor said
"another application's credential, not read", so those providers read
nothing for operators who never mint an API key.

CodexBar reads all of them, without asking. That is the behaviour this ADR
refuses: an application that reads every sign-in it can find on a machine
is what credential-stealing malware looks like from the outside, and an
operator cannot tell the two apart.

The repository owner decided: TokenOps may read sign-ins other applications
already store, **only after the operator opts in, per provider**; and may
read a site's browser localStorage, **at setup only**.

## Decision

1. **One mechanism, described as data.** A provider's source lists the
   sign-ins it can be read with (`providers.AppLoginItem`): a JSON file and
   field paths, a dotenv file and variable, a token file, a SQLite database
   and one `SELECT`, a Keychain item, or a running process's command-line
   flag; plus the one host the token is sent to. `internal/infra/applogin`
   is the only code that reads them. A provider read only this way has
   `Credential: AppLogin`; one also read with a key lists them beside it.

2. **Nothing is read without a grant.** `tokenops vendor-usage setup <id>
   --use-app-login` finds the item without reading anything secret (a
   file's existence, a process's presence), prints exactly which file,
   Keychain item or process is read, which field, which host the token is
   sent to and that nothing secret is stored, and asks y/N. Only on yes is
   the item read, once, against the vendor. Only if the vendor accepts it is
   the grant recorded: `vendor_usage.grants.<id>` holds the application,
   kind, item, fields, host and time, never a token. `--revoke-app-login`
   removes it, and `vendor-usage status` lists every grant.

3. **A grant covers exactly what was shown.** The daemon reads a grant only
   while the provider's descriptor still names the same kind, item, fields
   and host. A release that would read another field, another file or send
   the token elsewhere does not inherit the grant: the item is not read,
   status says so, and the operator grants again. A grant cannot widen what
   is read either: a path outside the descriptor's is honoured only when it
   came from the application's own path variable at setup.

4. **Read-only, every time, never refreshed.** The daemon re-reads the
   granted item on each poll that needs it, so a token the owning
   application renewed is picked up. It opens files read-only, reads a
   database from a private copy, never writes the item, and never refreshes
   or rotates a token: refreshing would sign the owner out. An expired
   token is a refused reading; the stale-reading finding says to sign in
   with the owning application.

5. **Least invasive last.** A granted sign-in is tried only after every
   key setup stored, a harness sends or the environment holds was refused
   or absent.

6. **Keychain items only quietly.** A Keychain sign-in is read with
   `keychain.Quiet`, by setup and daemon alike: never a prompt (when macOS
   would ask, the read fails and health says so), and never at all with
   `keychain.disabled`.

7. **Tokens never leave the reader.** No error, log line, status row or
   setup output carries a token or any of the item's content: errors name
   the item and the field. Readers name the host and path they called,
   never a query string.

8. **Browser localStorage at setup only.** A session some sites keep in
   localStorage rather than a cookie (Devin, Windsurf) is read from a
   Chromium profile's `Local Storage/leveldb` by the interactive `setup
   <id>` only, from a private copy of the database (the browser holds a
   lock), for one origin and the keys the descriptor names. Chromium does
   not encrypt localStorage, so no Keychain item is read. The daemon never
   reads localStorage; what setup read is stored like a pasted session, and
   paste stays the fallback.

9. **No agent-facing tool grants or reads.** The MCP server neither offers
   `--use-app-login` nor reads localStorage: consent comes from a command
   the operator types, never from a chat.

## Consequences

- Providers with no API key of their own (Muse Code, Factory, Grok, Kiro's
  overage credits, Antigravity) read their usage once granted; providers
  with one (Kilo, Codebuff, ClinePass, Nous, Hugging Face) gain a second
  way.
- Devin and Windsurf read their web session from localStorage at setup
  instead of asking for a paste; the daemon still never reads a browser.
- The grant is per provider and per item; a provider with two sign-ins
  (Muse Code's file and Keychain item) grants the one setup found first.
- Antigravity's language-server token, read from its command line, was read
  without any grant; it now is read only after one.
- A descriptor change that touches an app login is a consent change:
  reviewers check that it is intended, since every operator who granted
  the old one must grant again.
