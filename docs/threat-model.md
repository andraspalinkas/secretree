# Threat model

This document is deliberately blunt. A backup tool that overstates its
guarantees is worse than none.

## Assets

| Asset | Where it lives | Protected by |
|---|---|---|
| Source history (objects, refs, messages, author identities) | inside age-encrypted bundles | age (X25519 → ChaCha20-Poly1305) |
| Out-of-repo state (config, databases, ledgers) | inside age-encrypted tarballs | age |
| Backup manifests (ref map, hashes, chain links) | age-encrypted, then signed | age + OpenSSH signature |
| Age identity (decryption key) | OS key store; printed recovery kit | key store ACL / physical custody |
| Signing key (Ed25519) | OS key store; printed recovery kit | key store ACL / physical custody |
| Vault metadata (`vault.json`: format version, recipients, allowed signers) | plaintext on the remote, signed | OpenSSH signature |

## What the remote learns

The remote is treated as **honest-but-curious at best, fully compromised at
worst**. Even a fully compromised remote learns only:

- that a vault exists, its random vault id and per-repo random ids;
- the *count* of backups, their approximate timestamps (commit times on the
  vault repo), and ciphertext sizes (roughly proportional to plaintext size);
- the public keys of recipients and the signing public key (`vault.json`).

It never learns branch names, commit ids, commit messages, file names, file
contents, author names, or which repositories the ids correspond to.

Traffic analysis (size deltas of incremental bundles reveal activity rhythm)
is **not** mitigated in v1. Padding is a possible v2 option.

## Adversaries and outcomes

| Adversary | Outcome |
|---|---|
| Remote provider breached (token theft, provider CVE, rogue employee) | Reads ciphertext only. Cannot forge a backup (no signing key). Can *delete* or *roll back* the vault — detected by `verify` via the hash chain and by `status` showing a stale last generation. |
| Attacker with the vault's push credential but not the keys | Same as above: can append garbage or delete. Garbage is rejected (bad signature); deletion is detected, not prevented. Use a protected branch so even the credential cannot rewrite history. |
| Attacker with the workstation while it is unlocked | **Game over for confidentiality.** They can read the source directly and can pull keys from the key store as the user. This tool does not protect the developer machine; nothing running on it could. |
| Attacker with the workstation while it is locked / disk image only | Keys in the OS key store are encrypted at rest; source on disk is protected only by the platform's disk encryption. Not this tool's job. |
| Lost or destroyed workstation | Restorable from the vault with the printed recovery kit. Losing the kit **and** every paired device = permanent loss. That is the deal with zero knowledge. |
| Malicious or buggy secretree release | Mitigated by the format being restorable with stock tools and by the restore proof being a *real* fetch-decrypt-rebuild, not a self-report. Not mitigated: a malicious build could exfiltrate keys. Pin releases; build from source. |
| Compromised CI runner (phases 3–4) | The runner holds the unlock key; if it is compromised the source is exposed. The runner is the trust root by design — that is why it must be *yours*. |

## Explicitly out of scope

- Protecting against a compromised developer machine or a compromised
  self-hosted runner. The trust root is the machine that holds the key.
- Hiding the *existence* of a vault or the activity rhythm.
- Availability. A remote can always refuse to serve. Keep two remotes.
- Secrets management. Application secrets should not be in the repo or the
  state tar in the first place; when they must be, they are just more
  plaintext to the tool and get the same protection, no more.
- Deniability, anonymity, or resistance to legal compulsion of the key holder.

## Crypto choices, briefly

- **age**, not OpenPGP: modern, small, audited, multi-recipient by design,
  scrypt-free for key recipients, Go reference implementation plus an
  independent Rust one (rage). Plugin protocol allows hardware keys later.
- **OpenSSH signatures**, not minisign or GPG: `ssh-keygen -Y sign/verify`
  ships on every macOS, Linux and Windows install, uses Ed25519, has a
  namespace to prevent cross-protocol reuse, and has an `allowed_signers`
  file format that doubles as our recipient roster.
- **Encrypt-then-sign.** Signatures cover ciphertext, so integrity of the
  whole chain can be verified without the decryption key (e.g. by a
  second device that only holds the signing public key, or by a future
  "canary" job). Plaintext integrity is provided by age's AEAD.
- **Separate keys** for encryption and signing. Rotating one does not force
  rotating the other; recipients (many) and signers (few) have different
  lifecycles.
- **No passphrases as the primary key.** The author's earlier shell-script
  predecessor used a single PBKDF2 passphrase; it worked, but one shared secret has no
  revocation, no rotation, and no per-device identity. Passphrases remain
  as an *optional* wrapper around the printed recovery kit.
