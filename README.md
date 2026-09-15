# secretgit

**Private git for people who do not trust their git host.**

Your source code is plaintext only on machines that hold a key. Everything
that leaves them is ciphertext: history, branches, commit messages, pull
requests, review comments, CI logs, artifacts. GitHub, GitLab, an S3 bucket
or a folder on a NAS becomes a dumb, durable blob store plus a webhook. A
breach of the host yields ciphertext; a curious host learns sizes and timing.

And the developer experience stays what it is: `git push`, `git pull`, open
a PR, get a review, watch CI go green, deploy. Reviews and CI do not need a
server that can read your code; they need data in git and compute on a
machine that holds the key. How that works, layer by layer, is in
[docs/vision.md](docs/vision.md).

## Status

Layer 1 of five is built: the **vault** (Go, single static binary):
`init`, `backup`, `verify`, `restore`, `status`, `schedule` against any git
remote or a local directory, with a mandatory restore proof after every
backup. The on-remote format is specified in
[docs/vault-format.md](docs/vault-format.md) and frozen at v1, because the
format outlives the tool: a vault must stay restorable with nothing but
`git`, `age` and `ssh-keygen` ([docs/restore-by-hand.md](docs/restore-by-hand.md)).
The same chain is the sync primitive for everything above it.

Next: the git remote helper (`git push` straight into the vault, multiple
writers, team keys), then PRs and reviews as encrypted git data with a local
UI, then a runner and deploy agent. Roadmap in [docs/vision.md](docs/vision.md).

## Quick start

```bash
export PATH="$HOME/sdk/go/bin:$PATH"          # wherever your Go lives
go build -o bin/secretgit ./cmd/secretgit

cd ~/code/myrepo
secretgit init --vault git@gitlab.com:you/myrepo-vault.git --kit-out ~/Desktop/kit.txt
#   → keys in the OS key store, vault bootstrapped, recovery kit written. PRINT IT.
secretgit backup            # full bundle → age → signed manifest → push → restore proof
secretgit status
secretgit schedule --every 1h
```

Out-of-repo state (config files, SQLite databases, ledgers) goes into an
encrypted archive alongside the bundle; configure it in
`.git/secretgit/config.json`:

```json
"state": {
  "include": ["config/config.yaml", "data/ledger.jsonl"],
  "exclude": ["*.tmp"],
  "pre_hook": "sqlite3 data/state.db \".backup '$SECRETGIT_STAGE/data/state.db'\""
}
```

Disaster: on a fresh machine with only the recovery kit,

```bash
secretgit restore --vault git@gitlab.com:you/myrepo-vault.git --to ~/code/myrepo --from-recovery-kit kit.txt
```

## Non-negotiables

1. **A silent, unrestorable backup is the only unacceptable failure.** Every
   backup ends with a restore proof fetched back *from the remote*; until it
   is green the tool does not say "done".
2. **No home-grown crypto.** Encryption is [age](https://age-encryption.org)
   (X25519 + ChaCha20-Poly1305), signatures are OpenSSH signatures
   (`ssh-keygen -Y sign`, Ed25519). Both are ubiquitous and auditable.
3. **The recovery path never depends on secretgit.** Docs and a plaintext
   README are stored in the vault itself, and use only stock tools.
4. **Keys never leave the key store in plaintext at rest.** macOS Keychain,
   Linux Secret Service, Windows Credential Manager. Hardware-backed when
   the platform allows it.
5. **Append-only remote.** Every backup is a new commit on a protected branch.
   A compromised workstation cannot rewrite what was already backed up.

## Commands

```
secretgit init      # keys → key store, printable recovery kit, vault repo bootstrap or join
secretgit backup    # full or incremental bundle + optional out-of-repo state → encrypt → sign → push → restore proof
secretgit verify    # fetch the chain back from the remote, check signatures, hashes, chain, and rebuild in a temp dir
secretgit restore   # rebuild a repo (and its state) from the vault at any generation
secretgit status    # last backup, last *proven* restore, chain size
secretgit schedule  # launchd (macOS) or systemd user timer (Linux)
```

## Documents

- [Vision and architecture](docs/vision.md) — what "no loss of developer
  experience" means, and how PRs, reviews and CI work without a server that
  can read the code.
- [Threat model](docs/threat-model.md) — what the tool does *not* protect against.
- [Vault format v1](docs/vault-format.md) — what lands on the remote and why.
- [Restore by hand](docs/restore-by-hand.md) — the disaster path, no secretgit needed.
- [Decisions](docs/decisions.md) — architectural decision log.

## License

MIT. See [LICENSE](LICENSE).
