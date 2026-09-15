# Product vision and architecture

**secretgit: private git for people who do not trust their git host.**
Source code is plaintext only on machines that hold a key. Everything that
leaves those machines is ciphertext: history, branches, commit messages,
pull requests, review comments, CI logs, build artifacts. The host you
already use (GitHub, GitLab, Codeberg, an S3 bucket, a NAS) becomes a dumb,
durable, globally reachable blob store plus a webhook. And the daily
developer experience stays `git push`, `git pull`, open a PR, get a review,
watch CI go green, deploy.

This document is the map. It says what "no loss of developer experience"
means concretely, which parts of a normal forge can be kept, which cannot,
and how each layer works. Status: layer 1 is built; the rest is design.

## Why

Git hosts are breached through stolen tokens, provider CVEs, malicious
integrations and insiders. Every one of those exposes plaintext source.
Teams who care either self-host a forge (and now run a server, patch it,
back it up, expose it to the internet) or accept the risk. secretgit is the
third option: keep the convenience of a hosted remote, remove its ability to
read anything.

Existing tools cover pieces: git-remote-gcrypt (encrypted remote, GPG,
no collaboration story, re-uploads packs), git-crypt/transcrypt (file
contents only; names, structure and history are visible), restic/borg
(great backups, not git-native), Keybase git (had the right idea, stalled).
None of them answer "how do I do a code review".

## What "no DX loss" means

| A developer does… | With secretgit |
|---|---|
| `git clone`, `git push`, `git pull`, branches, tags | Identical. A remote helper (`secretgit::<url>`) encrypts on push and decrypts on fetch. |
| Opens a pull request, requests review | `secretgit pr open` or the local UI. PR metadata is a git object like everything else, encrypted. |
| Reads a diff, leaves line comments, approves | In the local UI (a small web app on `localhost`, or an IDE extension). Comments sync through the same encrypted remote. |
| Gets notified | Push notifications carry only "PR #12 changed" plus an opaque id; the body is fetched and decrypted locally. |
| Sees CI status on a PR | A runner *you* control decrypts the source, runs the pipeline, writes an encrypted result back; the UI shows it. |
| Deploys | A deploy agent on the target host pulls the encrypted artifact, verifies the signature, decrypts in memory. |
| Searches code, blames a line, shares a permalink | Local UI and IDE. Permalinks are `secretgit://` links that resolve on any machine with the key. |
| Files an issue | Same mechanism as PRs (git-native, encrypted). Optional, later. |

What is genuinely lost, stated plainly: the host's own web UI for code, the
host's hosted runners at scale, the host's marketplace of integrations that
read source, and the "anyone with the link can view" convenience. Those are
the things that require the host to read your code; that is the point.

## Architecture: five layers on one primitive

The primitive is the **vault chain** from `docs/vault-format.md`: an
append-only sequence of signed manifests and age-encrypted git bundles.
It was built as a backup format. It is also, unchanged, a *sync* format:
a push is "append a generation", a fetch is "replay the generations I have
not seen". Every layer above stores its data as git objects and therefore
rides the same chain.

```
┌─────────────────────────────────────────────────────────────┐
│  5. hosted E2E web app / confidential compute   (much later)│
├─────────────────────────────────────────────────────────────┤
│  4. runner + deploy agent        encrypted CI logs, signed  │
│                                  artifacts, pull-based CD   │
├─────────────────────────────────────────────────────────────┤
│  3. collab: PRs, reviews, checks  git-native, encrypted,    │
│     + local UI / IDE extension    notifications by webhook  │
├─────────────────────────────────────────────────────────────┤
│  2. sync: git remote helper       git push/fetch on the     │
│     + team keys                   chain; multi-writer CAS   │
├─────────────────────────────────────────────────────────────┤
│  1. vault: chain format, backup,  built ✔  (secretgit 0.1)  │
│     verify, restore proof, keys                             │
└─────────────────────────────────────────────────────────────┘
        dumb storage: GitHub / GitLab / S3 / rclone / a folder
```

### Layer 1 — vault (built)

Format, backup, mandatory restore proof, key store, recovery kit,
scheduling. Nothing in the upper layers changes the format; they add
*content* (more refs) and *writers* (more signers).

### Layer 2 — sync: the remote helper

`git remote add origin secretgit::git@github.com:team/app-vault.git`.

The helper keeps a local plaintext **mirror** (a bare repo under
`.git/secretgit/mirror`) that represents the vault's logical state. It
advertises git's `connect` capability and proxies git's native protocol to
`git-upload-pack` / `git-receive-pack` running on the mirror:

- **fetch**: sync the chain from the remote (only new generations are
  downloaded), replay them into the mirror, then let git fetch from the
  mirror. The chain is verified end to end on every sync, exactly as
  `verify` does today.
- **push**: sync first, let git push into the mirror, then produce one new
  generation from the mirror (incremental bundle against the last
  generation's ref map) and push it to the vault. The host's rejection of a
  non-fast-forward push is the compare-and-swap that serialises concurrent
  writers: on rejection the mirror is reset to the vault's truth and the
  user sees the familiar "fetch first" error.

Cost model: a push is one small bundle upload; a fetch downloads only unseen
generations. Periodic full generations bound replay length for fresh
clones. Reflog-style history of the chain is free (it is the vault repo's
git history), so "what did the remote look like last Tuesday" is
answerable and restorable.

Team keys: `vault.json` lists **recipients** (who can read) and **allowed
signers** (which devices can write). Adding a member re-encrypts nothing
old (they get history from the next full generation onward, or an explicit
re-encrypt run); removing one triggers a full generation to the new
recipient set and a signer removal. Invitations are a pasteable public key,
approved and signed by an existing member. Per-device keys, not per-person
passphrases, so a lost laptop is revoked, not rotated.

### Layer 3 — collaboration: PRs and reviews without a server

The insight that makes this work: a pull request is data, not a service.
Title, description, base and head refs, review comments anchored to a
commit + path + line, approvals, status checks, labels, state transitions.
All of it fits in git objects under a dedicated ref namespace
(`refs/secretgit/collab/*`), synced through the same chain, encrypted with
the same keys, signed by the author's device key so authorship is
verifiable without a server. Prior art: git-appraise (Google) and git-bug
store reviews and issues exactly this way; secretgit adopts the approach
and keeps the data format documented so other clients can read it.

Where people *look* at it:

- **Local UI**: `secretgit ui` serves a small web app on `localhost` with
  the views developers expect: PR list, diff with line comments, review
  threads, approve/request changes, CI status, blame, search. It reads the
  mirror and writes collab objects; it never listens on a public interface.
- **IDE extension** (VS Code first): the same views inside the editor,
  which is where most people review anyway.
- **CLI**: `secretgit pr open|list|show|approve|merge`, `secretgit review`.

Notifications: the host sees that the vault repo received a push and can
fire a webhook; a tiny relay (or the host's own notification) tells team
members "something changed" with an opaque id. The client fetches and
decrypts. Nothing readable transits the notifier. Optional e-mail/ntfy.

Merging: `secretgit pr merge` performs the merge locally (fast-forward,
merge commit or squash, as configured), pushes it through the helper, and
records the state change in the collab data. Branch protection rules
("needs 1 approval and green CI") are enforced by the client and *verified*
by every other client: because approvals and check results are signed
objects, a merge that violates policy is detectable by everyone, not just
preventable by a server.

Alternative kept in the docs for teams that already run a server: a
self-hosted forge (Forgejo) behind the key, with secretgit as the encrypted
off-site sync and backup of it. Zero-knowledge toward the cloud, ordinary
forge DX, but you run a server again.

### Layer 4 — CI/CD: compute goes where the key is

Rule one: the key never enters the host's "secrets" store. A secret that
the host can hand to a hosted runner is a secret the host can read.

- **Runner agent** (`secretgit runner`): a daemon on a machine you own (a
  Mac mini, a home server, a VPS, a spare laptop). It watches the vault
  (webhook or polling), fetches new generations, decrypts the needed commit
  into a RAM-backed directory, and executes the pipeline. Pipelines are the
  team's *existing* files: `.github/workflows/*.yml` run through `act`,
  `.gitlab-ci.yml` through gitlab-runner in exec mode, or a plain
  `Makefile`/`justfile`. Logs and artifacts are encrypted and appended to
  the vault as check results; the local UI renders them. Scale out by
  running more runners; each holds a device key with a "runner" role that
  can read and sign check results but not merge.
- **Hybrid** for teams who want the host's status UI: register the runner
  as a GitHub/GitLab self-hosted runner. The host then sees the workflow
  file and job names; the checkout step is replaced by `secretgit unlock`,
  which refuses to run on a hosted runner. Log privacy levels: plain,
  redacted, encrypted-only.
- **Artifacts**: signed with the vault's signing key (or cosign) and
  encrypted to the deploy target's key.
- **Deploy agent** (`secretgit deploy-agent`): runs on the target host,
  pulls the encrypted artifact or the source generation, verifies
  signature and policy ("only artifacts from a merged, approved, green
  commit"), decrypts in memory, applies. Pull-based, so the target needs no
  inbound access and the CI machine holds no production credentials.
  Environment secrets are age-encrypted to the target's key in the repo.

### Layer 5 — for people without a machine

A hosted end-to-end-encrypted web app (keys stay in the browser, like the
E2E mail providers) and attested confidential compute for runners. Both
shift trust to served code or to hardware vendors; worthwhile only after
layers 2–4 exist and only as an explicit, documented trade-off.

## Threat model deltas for a team product

Everything in `docs/threat-model.md` holds. With a team, add:

- Every member's device is now a trust root. A compromised laptop exposes
  what that laptop can read. Per-device keys make revocation possible;
  they do not make the past unread.
- The host learns team size (recipient count), activity rhythm and object
  sizes. It does not learn who pushed what: signer identities are inside
  the encrypted manifests, only the signing *public keys* are on the
  remote, and those can be rotated per device.
- Review comments and CI logs are as sensitive as source; they are treated
  as source.

## Roadmap

1. **Vault** — done (0.1): format, backup, verify, restore proof, keys, schedule.
2. **Sync** — remote helper with `connect`, multi-writer CAS, team recipients
   and signers, invitations, device revocation, `secretgit clone`.
3. **Collab** — documented PR/review data model, CLI, local UI (PR list,
   diff, comments, approvals), webhook notifier, policy verification.
4. **Runner + deploy** — runner agent with `act`/make pipelines, encrypted
   logs and artifacts, signed checks, deploy agent, environment secrets.
5. **IDE extension, S3/rclone targets, hardware-backed keys, hosted E2E app.**

The order is deliberate: each layer is useful on its own, and each is a
prerequisite for the next. A team can stop at 2 (private hosted git with a
normal workflow) or 3 (plus reviews) and already have something no forge
offers.
