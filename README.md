# secretgit

**Zero-knowledge, off-site git backup (and later: multi-device sync and CI/CD)
on top of any dumb storage you already have — a free git host, a folder, S3.**

The host never sees plaintext: not the code, not the history, not the commit
messages, not even the branch names. It stores opaque, authenticated blobs and
an append-only chain of signed manifests. If the host is breached, the attacker
gets ciphertext. If the host is curious, it learns only sizes and timing.

secretgit does not replace a git host. Code review, issues, hosted CI: gone.
What you keep is a git-native, incremental, *proven-restorable* backup whose
trust root is your own machine and your own key store. The realistic full
picture is a local or self-hosted forge for collaboration plus a cloud host as
a blind mirror.

## Status

Working MVP (Go, single static binary): `init`, `backup`, `verify`,
`restore`, `status`, `schedule` against a git remote or a local directory.
The on-remote format is specified in [docs/vault-format.md](docs/vault-format.md)
and frozen at v1, because the format outlives the tool: a vault must stay
restorable with nothing but `git`, `age` and `ssh-keygen`.
See [docs/restore-by-hand.md](docs/restore-by-hand.md).

Not yet: multi-device pairing, extra recipients and rotation, S3/rclone
targets, the git remote helper, notifications, Linux/Windows key stores
(Linux falls back to a 0600 file under `~/.config/secretgit`).

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

Later phases: `git push vault main` via a remote helper, multi-device pairing,
team recipients and rotation, a runner agent that unlocks source into tmpfs
for CI, pull-based CD agents on target hosts, and eventually attested
confidential compute for people without a machine of their own.

## Documents

- [Threat model](docs/threat-model.md) — read this first; it says what the
  tool does *not* protect against.
- [Vault format v1](docs/vault-format.md) — what lands on the remote and why.
- [Restore by hand](docs/restore-by-hand.md) — the disaster path, no secretgit needed.
- [Decisions](docs/decisions.md) — architectural decision log.

## License

MIT. See [LICENSE](LICENSE).
