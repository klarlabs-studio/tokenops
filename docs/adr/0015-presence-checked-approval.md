# ADR 0015 — Presence-checked boundary approval: an approval is signed by a key only a person present can use

- **Status:** Accepted 2026-10-24 (proposed 2026-10-22)
- **Date:** 2026-10-22
- **Deciders:** TokenOps maintainers
- **Related:** ADR 0014 (action boundaries; "Only a person approves"),
  ADR 0007 (not a hard control), ADR 0010 (the daemon API serves every
  surface), ADR 0004 (shadow before enforcement),
  `web/docs/guide/boundaries.md` § *What approval guarantees* (#702)

## Context

ADR 0014 says only a person approves a boundary. Today that is enforced by
the channel an answer arrives on, not by anything the answer carries:

- `internal/capability/boundaries/decide.go`: `approvesInPerson` lets only
  `cli` and `prompt` approve. The MCP tool and
  `POST /api/boundaries/decisions` can only deny.
- `internal/cli/boundaries.go`: `tokenops approve` refuses when stdin is not
  a terminal (`approverAtTerminal`).
- The menu bar cannot approve over the API; it opens Terminal running
  `tokenops approve <id> --confirm` (`apps/menubar/approve.go`).
- An answer is a JSON file, `~/.tokenops/boundaries/answers/<id>.json`
  (`boundaryledger.Answer`). The pause point reads it, honours it, and for a
  one-step answer consumes it (`guard.answered`). Nothing in it shows who
  wrote it; `Method` is whatever the writer put there.
- A `prompt` answer is inferred: the pause point sees in Claude Code's
  transcript that the tool call it asked about ran (`settlePrompt`).

#702 documented the gap honestly. An agent running as the operator, with a
shell, can approve in the operator's place:

1. `script -q /dev/null tokenops approve <id>` gives the CLI a terminal
   (so would `expect` or `tmux`).
2. It can read the dashboard token (`~/.tokenops/dashboard.token`, also in
   `daemon.url`) and call the daemon's settings endpoints:
   `POST /api/mode`, `POST /api/routing/rules`,
   `POST /api/routing/decisions` (which does approve a routing upgrade),
   `POST /api/budgets`, `POST /api/coach`.
3. It can write `answers/<id>.json` itself, or edit `config.yaml` to demote
   or delete the rule, or uninstall the hook.

The repository is public, so the file formats are one read away. Path 1 is
the dangerous one: it is a single command an agent that is wrong or
misled, not malicious, can arrive at by itself after a refusal tells it
"run `tokenops approve`". Paths 2 and 3 take deliberate work, but nothing
records that they happened.

### Threat model

The adversary is a process running as the operator's user: the agent a
boundary stopped, a subagent, or something it started. It has the user's
files, the user's network, and a pseudo-terminal whenever it wants one. It
is **not** root, does not control the kernel, and cannot synthesise input to
a system authentication dialog (macOS does not accept synthetic events
there, and Touch ID cannot be faked from user space).

What such a process can **always** do, and no design here changes:

- Write any file the user can write: `config.yaml`, the boundary ledger,
  the hook entries in the harness's settings, the `tokenops` binaries
  themselves (Homebrew's prefix is user-writable).
- Kill and restart the daemon, so state the daemon holds in memory or in
  its store is only as durable as the files behind it.
- Call any local API whose credential is a file.

What a presence check **can** stop: an approval reaching the pause point,
the router or the uploader that no person present at the machine gave.
Not by detecting who typed, which #702 showed cannot be done, but by
requiring a proof only a present person can produce, and refusing answers
without one. The remaining routes around it are no longer "answer
differently"; they are "forge TokenOps' key store or replace its binaries",
which is deliberate, multi-step, and leaves a trace this ADR makes visible.

### What the platform allows without an Apple Developer ID

TokenOps has no Apple Developer ID: the menu bar is ad hoc signed and
reaches users through the Homebrew cask, whose post-install hook clears
quarantine. The darwin CLI and daemon are built with `CGO_ENABLED=0`.
A spike on 2026-10-22 (macOS 27, Apple M5, Swift 6.4, binaries
linker-signed ad hoc, no entitlements) measured:

| What | Result |
|---|---|
| `LAContext.canEvaluatePolicy` (`.deviceOwnerAuthentication`, biometrics) | works |
| Data-protection Keychain item with a `.userPresence` access control (`SecItemAdd`, `kSecUseDataProtectionKeychain`) | **`-34018` errSecMissingEntitlement** |
| CryptoKit `SecureEnclave.P256.Signing.PrivateKey(accessControl: [.privateKeyUsage, .userPresence])`, `…AfterFirstUnlockThisDeviceOnly` | **works**; the key is an opaque blob (≈430 bytes) the caller stores itself |
| Same, `…WhenUnlockedThisDeviceOnly` | `-25308` from the agent's (non-GUI) shell; to be re-run from a logged-in GUI session |
| The blob written by one binary, loaded by another | works: the key is bound to this Mac's Secure Enclave, not to the creating binary's signature |

Signing with the key (which raises the Touch ID or password dialog) was not
exercised: it would have prompted the operator mid-task. It is the first
item of the validation plan.

So: a Keychain item guarded by user presence (option b as first framed)
needs an entitlement, and so a Developer ID. A Secure Enclave key whose
every signature needs user presence does not. Since the blob is not bound
to the binary, an ad hoc re-sign on upgrade does not lose the key, and any
of the user's processes can *ask* for a signature, each one raising the
system dialog.

## Options

**(a) LocalAuthentication as a gate.** `tokenops approve` and the menu bar
call `evaluatePolicy` (via cgo, a helper, or `osascript -l JavaScript` with
`ObjC.import('LocalAuthentication')`) before writing the answer.
Stops path 1: `script … tokenops approve` now raises a dialog. Does not
stop path 3, writing the answer file, because the check happens in the
writer and the reader cannot tell. The `osascript` route needs no build
change but runs an asynchronous callback API from a script, and is the most
fragile of the three; cgo would move the darwin CLI build to a macOS runner.
A gate on its own is a better terminal check, not a proof.

**(b) A presence-bound signing key; answers carry signatures.** A Secure
Enclave P-256 key, created with `.userPresence`, signs each approval. The
pause point, the router, the uploader and the daemon verify the signature
against the enrolled public key, in pure Go, and do not honour an approval
without one. Covers paths 1 and 3 for approvals, and makes the API safe to
accept approvals on (the proof is in the answer, not the channel). The
Keychain variant is out (entitlement); the CryptoKit blob variant works ad
hoc. Needs a small Swift helper, built on the macOS runner that already
builds the menu bar. Verification adds well under a millisecond to the
pause point's 5 ms budget (ECDSA P-256 verify is tens of microseconds).

**(c) Approval in the menu bar only.** Removes the CLI path, and with it
`script`. But the menu bar would still write an unsigned answer file any
process can write, so on its own it closes path 1 only. It also leaves SSH
sessions and Linux with no way to approve at all.

**(d) Status quo plus documentation.** What #702 shipped. Honest, and
cheap; leaves the one-command path open to an agent that is merely wrong.

**Linux.** There is no Secure Enclave. polkit can authenticate the user
(`pkcheck` with an `auth_self` action), but the action file must be
installed under `/usr/share/polkit-1/actions` by root, which a user-level
install does not have, and the answer is a process exit code, the same
gate as (a). `fprintd` verifies a finger and produces nothing a verifier can
check later. A TPM-bound key with a PIN policy is a proof but a large
dependency for a small audience. A FIDO2 security key is a real
presence-bound signer on every OS, reachable without cgo through
`ssh-keygen -Y sign` with an `ecdsa-sk`/`ed25519-sk` key, and verifiable in
Go (SSHSIG); it needs hardware the operator owns.

## Decision

**Adopt (b), with (a) as its user experience and (c) as its primary
surface, on macOS. Linux keeps today's guarantees, documented as such, with
a FIDO2 provider as a later, opt-in phase. (d)'s documentation is rewritten
to match what each machine actually enforces.**

### 1. Enrolment: one presence key per machine

`tokenops presence enroll` (also offered by the menu bar and the setup
wizard on a Mac with a Secure Enclave) runs `tokenops-presence`, a Swift
helper of about 150 lines, which:

- creates a `SecureEnclave.P256.Signing.PrivateKey` with
  `[.privateKeyUsage, .userPresence]`, `…ThisDeviceOnly`;
- signs an enrolment statement (key id, machine, user, time) with it, so
  enrolling itself raises the dialog once and proves the key works;
- writes the blob and the public key to `~/.tokenops/presence/`.

The daemon records the enrolled key's id in its store and as a decision
event (`presence_enrolled`). `.userPresence` means Touch ID, a paired Apple
Watch, or the login password: a Mac without Touch ID, or with its lid
closed, gets the password dialog. Nothing is enrolled automatically on a
Mac where the dialog cannot be shown.

The helper ships as its own binary in the darwin archive (beside `tokenops`
and `tokenopsd`, ad hoc signed, cleared by the cask's post-install hook) and
inside `TokenOps.app`. The CLI and daemon stay `CGO_ENABLED=0`; they only
exec the helper and verify in Go. A replaced helper gains nothing: it
cannot produce a signature under the enrolled key without the dialog.

### 2. An approval is a signed statement about one decision

An approval carries a `PresenceProof` (a value object in
`internal/contexts/boundaries`):

```
v=1  key=<key id>  decision=bnd-…  rule=<name>  session=<id>
scope=once|session  challenge=<pending decision's nonce>
crossed=<hash of the crossings shown>  issued=<RFC 3339>
```

signed with the enrolled key. Each pending decision gets a fresh random
`challenge` when it is raised, so a signature answers exactly that raising
of that decision. The dialog's reason text names what is approved: the
decision, the rule, what crossed, and the scope.

Verification is pure Go and checks the key, every bound field, and age: a
one-step approval is honoured for 10 minutes after it was signed, a session
approval within the existing 24-hour `answerTTL`. Denials need no proof; a
deny only tightens, and stays available everywhere it is today.

### 3. Which surfaces honour what

With presence enforced on a machine (§5):

| Surface | Approve | Deny |
|---|---|---|
| `tokenops approve <id>` | yes, signs through the helper (dialog); the terminal check stays as a first filter | yes |
| Menu bar | yes, **inline**: Approve raises the dialog and posts the signed answer; it no longer opens Terminal | yes |
| `POST /api/boundaries/decisions` | **yes, if the body carries a valid proof**; unsigned approvals are refused as today | yes |
| MCP `tokenops_boundaries` | no (unchanged: an agent has no reason to carry a proof) | yes |
| Claude Code's permission prompt (`prompt`) | yes, labelled `prompt` (harness-attested); see decision 2 | yes |
| An answer file without a valid proof | **not honoured**; recorded as `unsigned_answer_ignored` | honoured |
| Team-plane routed rules | unchanged (ADR 0014 phase 3: answered by the role in the team plane) | yes |

The same proof format covers routing upgrade approvals
(`POST /api/routing/decisions`, `routing_decide`), which today approve over
the API token: with presence enforced, an upgrade approval without a proof
is refused there too.

`DecisionApproval` gains `Presence` (`signed`, `key_id`, or `harness` for a
prompt), so `tokenops explain <id>` says whether a person was proven present
or a harness vouched for them.

### 4. Settings and rules: what is covered and what is not

The settings endpoints (`/api/mode`, `/api/routing/rules`, `/api/budgets`,
`/api/coach`, `/api/preferred-models`) are **not** put behind presence by
this ADR. Each writes `config.yaml`, which the same process can edit
directly; a signed API in front of an unsigned file would be a lock on a
door beside an open wall. They stay as they are: the MCP settings tools ask
at the client, the API takes the token, and the guide says plainly that a
same-user process can change settings.

Boundaries are the part that must bind, so they get the stronger answer in
a later phase (§7, phase 5): a **sealed rule set**. `tokenops boundaries
seal` signs the current local rules with the presence key; the pause point
applies the strictest of the sealed set and `config.yaml`, the same merge
organisation rules already use (ADR 0014 phase 3). Tightening a rule takes
effect at once; loosening one (demote, delete, raise a threshold, remove a
hard budget window) takes effect only once resealed, which raises the
dialog. Settings that are not boundaries stay settings; an operator who
wants routing to bind writes a `model_route` or `subagent_model` boundary.

### 5. Rollout: shadow, then opt-in enforcement, then the default

- **Shadow (first release).** Approvals are signed wherever the helper and
  an enrolled key exist; nothing is refused for lacking a proof. The pause
  point records `unsigned_answer_would_be_ignored` with the method, and
  `tokenops boundaries status` shows the count, so an operator sees whether
  anything on the machine approves without them.
- **Opt-in enforcement.** `boundaries.approval: presence` in `config.yaml`
  (or `tokenops presence enforce`, which sets it and needs the dialog).
- **Default.** One release later, on a Mac with an enrolled key, enforcement
  is the default; `boundaries.approval: channel` keeps today's behaviour and
  `boundaries status` labels it.

**Existing ledgers.** Nothing is migrated or re-signed. Decision history
keeps its recorded method; `Presence` is empty for answers given before the
change and `explain` says "before presence checks". Answers on disk at the
moment enforcement is turned on are honoured until they expire (at most
24 hours), with their method shown; pending decisions simply need a signed
answer.

### 6. Where an approval cannot be given

- **No Secure Enclave** (Intel Macs without a T1 or T2 chip), **Linux**, **no enrolled
  key**: approval works as today (terminal and prompt), and
  `tokenops presence status`, `boundaries status` and the guide say the
  machine has no presence check. If `boundaries.approval: presence` is set
  anyway (say, shipped by device management), approvals are unavailable:
  an `ask` or `approver:` rule in enforce stage refuses with the way out
  ("approve from a machine with presence, or demote the rule"); deny,
  shadow rules and the team plane are unaffected.
- **SSH sessions, launchd jobs, CI.** The dialog needs the console user's
  GUI session. The helper fails fast with "no one can be asked here" rather
  than waiting; the boundary stays refused. Headless approval is a non-goal:
  a headless approval is exactly what this ADR removes.
- **Key lost** (new Mac, Secure Enclave reset, blob deleted). `tokenops
  presence enroll --replace` enrols a new key with the dialog. A key change
  not signed by the previous key starts a 15-minute hold, during which
  approvals are refused, and is shown on the glance, in the menu bar and in
  `boundaries status` as "approval key replaced at …". A rotation signed by
  the old key (`enroll --rotate`, two dialogs) has no hold.

### 7. What stays out of reach

- **Root, the kernel, or anyone who can replace binaries.** A process that
  swaps `tokenops` for a build that skips verification, or replaces the
  enrolled public key with one of its own, gets through. The verifier
  cannot tell a Secure Enclave key from a software key (attestation for
  Mac apps needs a Developer ID). What changes is the trace: a key
  replaced without the old key's signature is held and announced (§6), and
  the daemon records it.
- **A person who approves a dialog they did not expect.** Any of the user's
  processes can ask the Secure Enclave for a signature, and the dialog's
  reason text is the caller's to write. A forged request still needs a
  person's finger or password at that moment; the guide says to approve
  only a dialog you started.
- **A user who turns it off** (`boundaries.approval: channel`), removes the
  hook, or edits `config.yaml` before rules are sealed. Turning enforcement
  off is a loosening, so once phase 5 lands it needs the dialog too.
- **Replay within a decision's window.** A captured one-step signature
  could be replayed against the same decision raising if the pending file
  is also forged back to its old challenge, for at most 10 minutes. The
  daemon flags a proof seen twice.

It remains a guardrail, as ADR 0007 and 0014 say. The difference is that
the guardrail can no longer be stepped over by the one command an agent is
told to run.

## Consequences

- `tokenops approve` and the menu bar raise a Touch ID or password dialog
  per approval. A session-scoped approval keeps that to once per session
  per rule; a dialog is never reused across approvals
  (`touchIDAuthenticationAllowableReuseDuration` stays 0).
- The menu bar becomes the natural place to approve: no Terminal window,
  one dialog. Its `boundaries.approve` permission now posts a signed answer
  through the daemon instead of opening a script.
- The darwin release builds one more binary on the macOS runner; the CLI
  and daemon stay pure Go. Linux packaging is unchanged.
- New domain value object (`PresenceProof`) and verifier in
  `internal/contexts/boundaries`; key store in
  `internal/infra/presence`, registered in archlint; `DecisionApproval`
  gains `Presence` in `pkg/eventschema` (and its protobuf).
- The pause point gains a signature check on the path where an answer
  exists; its benchmark gate (< 5 ms) covers it.
- The guide's *What approval guarantees* is rewritten per machine state:
  presence enforced, presence in shadow, or channel only.

## Not doing

- Detecting a "real" terminal: #702's reasoning stands.
- A Keychain item with a presence access control: it needs an entitlement
  this project cannot sign for.
- Presence on settings endpoints that write `config.yaml` (§4).
- Headless or remote approval of a presence-enforced boundary.
- polkit or fprintd on Linux: they gate, they do not prove.

## Validation plan

1. **Spike, signing (before phase 2).** From a GUI session, with the ad hoc
   helper: signing raises the dialog; Touch ID, Apple Watch and password
   each succeed; Cancel fails; a second signature raises a second dialog;
   `WhenUnlockedThisDeviceOnly` works from the GUI session (else keep
   `AfterFirstUnlock…`); over SSH and from a launchd job the helper fails
   fast; an upgrade (re-signed helper) still signs with the old blob;
   timing of one signature. Results are appended to this ADR.
2. **Verifier tests** (table-driven): unsigned, wrong key, wrong decision,
   rule, session, scope or challenge, altered crossings, expired, a
   one-step proof reused after consumption, a proof from before an
   unsigned key replacement.
3. **Bypass tests**, run against a real ledger in an isolated HOME (never
   the live launchd job): `script -q /dev/null tokenops approve` without
   the helper's signature is refused; `curl` to
   `/api/boundaries/decisions` and `/api/routing/decisions` with an
   unsigned approval is refused; a hand-written answer file is not
   honoured and is recorded; a replaced public key holds approvals for
   15 minutes and is announced.
4. **Shadow week** on the maintainer's machine: zero approvals that would
   have been refused besides those the operator gave by `prompt`, and the
   dialog count per day, before the default flips.
5. **Benchmark gate:** the pause point stays under 5 ms with a signed
   answer present.

## Phases

1. This ADR.
2. Signing spike (validation 1); the ADR is amended with its results, and
   with a decision on the open questions.
3. `PresenceProof`, verifier, key store, challenge on pending decisions,
   `DecisionApproval.Presence`, shadow reporting. No behaviour change.
4. `tokenops-presence` helper in the release (archive and app bundle);
   `tokenops presence enroll|status|enforce`; `tokenops approve` signs;
   the API accepts signed approvals (boundaries and routing upgrades); the
   menu bar approves inline. Opt-in enforcement.
5. Sealed boundary rule set; loosening and turning enforcement off need
   the dialog; enforcement on by default for enrolled Macs; guide rewritten.
6. (Demand-driven) FIDO2 provider through `ssh-keygen -Y sign`, for Linux
   and for Macs without a Secure Enclave.

## Decisions on the open questions (2026-10-24)

The maintainer accepted the proposed default for each:

1. **Trust anchor for the public key.** The key file plus the daemon's
   record. An unsigned replacement starts a 15-minute hold on approvals,
   announced on the glance, menu bar and status. A rotation signed by the
   old key has no hold.
2. **Claude Code's prompt under enforcement.** `prompt` approvals keep
   counting, labelled harness-attested.
   `boundaries.approval: presence-strict` drops them.
3. **When enforcement becomes the default.** One release after the opt-in,
   on enrolled Macs only, after a shadow period that shows what would have
   been refused.
4. **Linux.** Documented as channel-only until someone asks for FIDO2.
