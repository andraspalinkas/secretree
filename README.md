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

All five planned layers have a working first version (Go, one static
binary, integration-tested against real repositories):

- **Vault**: `init`, `backup`, `verify`, `restore`, `status`, `schedule`.
  Frozen v1 format ([docs/vault-format.md](docs/vault-format.md)), mandatory
  restore proof, hand-restorable with `git` + `age` + `ssh-keygen`
  ([docs/restore-by-hand.md](docs/restore-by-hand.md)).
- **Sync**: `git clone secretgit::<vault-url>`, `git push`, `git pull` through
  a remote helper. Several writers; conflicts are ordinary git conflicts.
- **Team**: `join` on a new device, `member add|remove|list`, per-device keys,
  revocation that keeps the past verifiable and closes the future.
- **Collaboration**: `pr open|list|show|comment|approve|request-changes|merge|close`,
  `.secretgit/policy.json` (required approvals and checks) enforced from signed
  events, a local UI (`ui`) with PR pages, code browsing, blame, search and
  permalinks (`link`), encrypted `share` pages and a signed disclosure `ledger`.
- **Runner and deploy**: `runner` executes the repository's own pipeline on a
  key-holding machine and records signed checks with logs; `deploy-agent`
  ships a branch to a target host when its check is green.

Not yet: IDE extension, S3/rclone targets, Linux/Windows key stores (Linux
uses a 0600 file under `~/.config/secretgit`), re-encrypting history for
members added later, notification relay. Roadmap in [docs/vision.md](docs/vision.md).
Website: [site/index.html](site/index.html).

## Quick start

```bash
export PATH="$HOME/sdk/go/bin:$PATH"          # wherever your Go lives
go build -o bin/secretgit ./cmd/secretgit && bin/secretgit install-helper

# a new project
cd ~/code/myapp
secretgit init --vault git@gitlab.com:you/myapp-vault.git --kit-out ~/Desktop/kit.txt
git remote add origin secretgit::git@gitlab.com:you/myapp-vault.git
git push -u origin main                       # encrypted generation + signed manifest
secretgit status

# a second device or a teammate
secretgit join --vault git@gitlab.com:you/myapp-vault.git --name laptop --out join.txt
#   ...send join.txt to a member, who runs:  secretgit member add --request join.txt
secretgit clone git@gitlab.com:you/myapp-vault.git myapp

# look at code the way you are used to
secretgit ui --open                            # http://127.0.0.1:7391/blob/main/src/x.go#L10
secretgit link src/x.go:10

# reviews, CI and deploys
secretgit policy --approvals 1 --checks ci     # commit .secretgit/policy.json on main
secretgit pr open --title "Add retry" --head feature/retry
secretgit pr approve 1 -m "lgtm"               # from another device
secretgit runner                               # CI agent on a machine you own
secretgit pr merge 1
secretgit deploy-agent --to /srv/app --cmd "systemctl restart app"   # on the target host

# show something to someone without a key
secretgit share src/x.go --expires 3d --note "for the auditor"
secretgit ledger                               # what left, when, who, why
```

Backups of a repository that is not synced through the helper (or of
out-of-repo state such as config files and databases) use `secretgit backup`;
see the state archive example below.

Out-of-repo state (config files, SQLite databases, ledgers) goes into an
encrypted archive alongside the bundle; configure it in
`.git/secretgit/config.json`:

```json
"state": {
  "include": ["config/config.yaml", "data/ledger.jsonl"],
  "exclude": ["*.tmp"],
  "pre_hook": "sqlite3 data/state.db \\".backup '$SECRETGIT_STAGE/data/state.db'\\""
}
```

Disaster: on a fresh machine with only the recovery kit,

```bash
secretgit restore --vault git@gitlab.com:you/myapp-vault.git --to ~/code/myapp --from-recovery-kit kit.txt
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
secretgit init            keys → key store, recovery kit, vault bootstrap or join
secretgit backup          snapshot every ref (+ state archive) → encrypt → sign → push → restore proof
secretgit verify          fetch the chain back, check signatures, hashes, chain; rebuild in a temp dir
secretgit restore         rebuild a repo (and its state) from the vault at any generation
secretgit status          last backup, last *proven* restore, chain size
secretgit schedule        launchd (macOS) or systemd user timer (Linux)
secretgit clone           git clone through the helper (imports a recovery kit if given)
secretgit install-helper  symlink git-remote-secretgit next to the binary
secretgit join            new device: own keys + a join request for a member to approve
secretgit member          list | add | remove | request
secretgit pr              open | list | show | comment | approve | request-changes | merge | close
secretgit policy          write .secretgit/policy.json (required approvals, required checks)
secretgit runner          CI agent: run .secretgit/ci (or make ci, or --cmd) per new commit, record signed checks
secretgit deploy-agent    pull-based CD: export a branch to a directory when its check is green
secretgit share           encrypted, self-contained HTML snapshot of a file or diff
secretgit ledger          every deliberate disclosure, signed and hash-chained
secretgit ui              local code browser and pull requests over the vault mirror
secretgit link            permalink into the local UI
```

## Documents

- [Vision and architecture](docs/vision.md) — what "no loss of developer
  experience" means, and how PRs, reviews and CI work without a server that
  can read the code.
- [Threat model](docs/threat-model.md) — what the tool does *not* protect against.
- [Vault format v1](docs/vault-format.md) — what lands on the remote and why.
- [Restore by hand](docs/restore-by-hand.md) — the disaster path, no secretgit needed.
- [Integrations](docs/integrations.md) — dependency updates, scanners, LLM
  review and previews as jobs on your runner.
- [Decisions](docs/decisions.md) — architectural decision log.

## License

MIT. See [LICENSE](LICENSE).
