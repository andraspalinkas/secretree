# Decision log

Format: short ADR entries, newest last. Status: **accepted** / **proposed** /
**superseded**.

## 0001 — Name: secretgit — accepted (2026-09-15)

Repository lives at `~/Workspaces/secretgit`. The predecessor is the
tradebot repository's `scripts/backup_gitlab.sh`, which stays untouched;
secretgit is an independent project.

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

## 0007 — Implementation language — proposed

Candidates:

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

Decision pending owner's answer. Everything in `docs/` is language-neutral.

## 0008 — Key stores — proposed

MVP: macOS Keychain (`security` CLI or Security.framework), item per key,
service `secretgit`, account `<vault-id>/<key-role>`. Phase 2: Linux
Secret Service (D-Bus), Windows Credential Manager. Hardware-backed keys
(Secure Enclave via an age plugin, TPM) are phase 2+.

## 0009 — MVP targets — proposed

Remote types in the MVP: any git remote (SSH or HTTPS URL) and a local
directory (which is just a bare git repo on disk, so it is the same code
path and the natural test fixture). S3/rclone targets are phase 2; the
format is designed so a target only needs "put file" and "list/get files".
