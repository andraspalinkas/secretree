# Decision log

Format: short ADR entries, newest last. Status: **accepted** / **proposed** /
**superseded**.

## 0001 — Name: secretgit — accepted (2026-09-15)

The predecessor was a ~100-line private shell script (git bundle, openssl
AES-CBC with a Keychain passphrase, an append-only mirror repo on GitLab).
secretgit is an independent, standalone open-source project.

## 0002 — License: MIT — accepted (2026-09-15)

Chosen over Apache-2.0 for simplicity and to match age/rage's dual licensing.
Trivial to change while there are no outside contributors.

## 0003 — Vault format v1 frozen before code — accepted (2026-09-15)

The format is the product; the tool is replaceable. v1 is specified in
[vault-format.md](vault-format.md) with a hand-restore procedure using only
`git`, `age`, `ssh-keygen` and `shasum`. Any change that breaks hand-restore
of an existing vault is a v2 and requires a migration command.

## 0004 — age for encryption, OpenSSH signatures for authenticity — accepted (2026-09-15)

Rationale in [threat-model.md](threat-model.md). Rejected: OpenPGP
(git-remote-gcrypt's choice; key management is the reason gcrypt never
became mainstream), minisign (an extra binary to install on the disaster
machine), home-grown AES-CBC + PBKDF2 (the predecessor; unauthenticated).

## 0005 — Encrypt-then-sign, signatures over ciphertext — accepted (2026-09-15)

Allows chain integrity checks without the decryption key. See threat model.

## 0006 — Append-only remote, protected branch — accepted (2026-09-15)

Inherited from the predecessor and kept: every backup is a new commit, the
tool never force-pushes. Growth is bounded by periodic new full bundles;
pruning old chains is a phase-2 concern and will be done by starting a new
vault branch, never by rewriting.

## 0007 — Implementation language: Go — accepted (2026-09-15)

Owner's decision after weighing the candidates below. Go 1.27 is installed
under `~/sdk/go` (no Homebrew on the machine); `PATH` is set in `~/.zshrc`.
Module path is the bare name `secretgit` until a public home is chosen.

Candidates were:

- **Go** (recommended in the handoff): one static binary per platform, the
  reference age implementation is a Go library (`filippo.io/age`), OpenSSH
  signature format has a Go implementation (`github.com/hiddeco/sshsig`),
  the git remote-helper protocol is plain stdin/stdout. Cost: the Go
  toolchain is not installed on the development machine; requires a
  download from go.dev (~75 MB, no Homebrew present).
- **Python 3.12 via uv**: already installed, matches the owner's other
  projects, `pyrage` wraps the audited Rust rage implementation, signing via
  `ssh-keygen -Y` subprocess. Cost: distribution is `uv tool install`, not a
  single binary; startup latency matters for a remote helper; less credible
  as a public security tool.
- **Rust**: rage is native, single binary, but no toolchain installed and
  slowest iteration for a weekend MVP.

Everything in `docs/` stays language-neutral.

## 0008 — Key stores — accepted for MVP (2026-09-15)

MVP: macOS Keychain (`security` CLI or Security.framework), item per key,
service `secretgit`, account `<vault-id>/<key-role>`. Phase 2: Linux
Secret Service (D-Bus), Windows Credential Manager. Hardware-backed keys
(Secure Enclave via an age plugin, TPM) are phase 2+.

## 0009 — MVP targets — accepted for MVP (2026-09-15)

Remote types in the MVP: any git remote (SSH or HTTPS URL) and a local
directory (which is just a bare git repo on disk, so it is the same code
path and the natural test fixture). S3/rclone targets are phase 2; the
format is designed so a target only needs "put file" and "list/get files".

## 0010 — Vault clones are partial and checkout-less — accepted (2026-09-15)

Both the persistent cache under `.git/secretgit/vault-cache` and the fresh
clone made for every restore proof use `git clone --no-checkout
--filter=blob:none`, falling back to a plain no-checkout clone when the
server rejects filters. Manifests and only the bundles a restore needs are
fetched on demand, so the proof costs a few small blobs plus one chain of
bundles, not the whole vault. New generations are committed from the index
(`git read-tree HEAD` + `git add`), which needs no checkout either. Local
bare vaults are created with `uploadpack.allowFilter`, `receive.denyDeletes`
and `receive.denyNonFastForwards` set.

## 0011 — Generations may carry no bundle — accepted (2026-09-15)

A generation whose only change is a ref move onto existing objects (branch
created, deleted or renamed) or a state-archive change has no bundle file;
the manifest's ref map is authoritative. `git bundle` refuses to write an
empty bundle, and the alternative (forcing a full) would bloat the vault for
nothing. A `full` generation always carries a bundle.

## 0012 — Plaintext scratch space lives under `.git/secretgit/tmp` — accepted, revisit (2026-09-15)

Bundles and state archives exist in plaintext for the duration of a run in
a 0700 directory inside the repository's git dir, on the same disk as the
source itself, so no new exposure is created. A RAM-backed location
(tmpfs, macOS `hdiutil` ram disk) is a planned hardening for the runner and
restore-on-foreign-machine cases, where the disk is not already trusted.

## 0013 — Scope: a private-git product, not a backup tool — accepted (2026-09-15)

Owner's direction: a standalone open-source product that keeps source code
encrypted everywhere outside key-holding machines *without* degrading the
developer experience, including pull requests, reviews and CI/CD. The vault
format and backup command stay as layer 1; the same chain becomes the sync
primitive for a git remote helper, and collaboration data rides on it.
Architecture and roadmap: [vision.md](vision.md).

## 0014 — Collaboration data lives in git, clients enforce policy — accepted (2026-09-15)

PRs, reviews, approvals and check results are signed git objects under
`refs/secretgit/collab/*`, encrypted and synced like source (the
git-appraise / git-bug approach). No server ever holds plaintext, and
policy (approvals, green CI before merge) is verifiable by every client
because the inputs are signed. The user-facing surface is a local web UI,
an IDE extension and the CLI. A self-hosted forge behind the key remains a
documented alternative, not the product.

## 0015 — CI runs where the key is; the host is a webhook — accepted (2026-09-15)

A runner agent on hardware the team controls decrypts into RAM and executes
the team's existing pipeline files (`act` for GitHub workflows, make/just).
Logs and artifacts go back encrypted. Registering as a host's self-hosted
runner is a supported hybrid with documented metadata leakage. Keys are
never placed in a host's secrets store.
