# Vault format v1

This is the contract between secretgit and the remote. It is designed so
that a vault can be verified and restored with `git`, `age`, `ssh-keygen`
and `shasum` alone. See [restore-by-hand.md](restore-by-hand.md) for the
procedure; this document explains the *why*.

## 1. Overview

A **vault** is a git repository (or, later, any key-value store) that holds
backups of one or more **source repositories**. Each backup of a source repo
is a **generation**: an encrypted git bundle, an optional encrypted state
archive, and an encrypted, signed manifest. Generations of one source repo
form a **chain**: a full bundle followed by incremental bundles that build on
it, then a new full, and so on.

Everything under the vault except `README.md` and `vault.json` is ciphertext.

```
<vault repo root>
├── README.md                          # plaintext restore instructions
├── vault.json                         # plaintext vault metadata (signed)
├── vault.json.sig
└── repos/
    └── <repo-id>/                     # random 16-hex id, one per source repo
        ├── 000001.manifest.age        # encrypted JSON manifest
        ├── 000001.manifest.age.sig    # OpenSSH signature over the ciphertext
        ├── 000001.bundle.age          # encrypted git bundle (full)
        ├── 000001.state.tar.age       # encrypted state archive (optional)
        ├── 000002.manifest.age
        ├── 000002.manifest.age.sig
        ├── 000002.bundle.age          # encrypted git bundle (incremental)
        └── ...
```

Generation numbers are zero-padded six-digit decimals, strictly increasing,
starting at `000001`, never reused. Every backup run is **one commit** on the
vault's default branch that adds exactly the files of one generation.
The tool never amends, rebases or force-pushes; configure the branch as
protected on the host so a stolen push credential cannot rewrite it either.

## 2. Keys and roles

| Key | Algorithm | Purpose | Holders |
|---|---|---|---|
| Recipient identity | age X25519 (`AGE-SECRET-KEY-1…` / `age1…`) | Decrypt manifests, bundles, state | Every device or person allowed to restore |
| Signing key | Ed25519 as OpenSSH key | Sign manifests and `vault.json` | Every device allowed to *write* backups |

Multiple recipients are native to age: every ciphertext is encrypted to all
recipients listed in `vault.json` at the time of writing. Adding a recipient
takes effect for new generations; making old generations readable to a new
recipient requires re-encrypting them (a phase-2 `reencrypt` command that
appends re-encrypted copies as new generations, never rewrites).

Signers are listed in `vault.json` in OpenSSH `allowed_signers` syntax so
that verification is a one-liner with stock tooling.

## 3. `vault.json`

Plaintext, UTF-8, JSON, signed by a listed signer. Example:

```json
{
  "format": "secretgit-vault/1",
  "vault_id": "3f9c2a1b7e5d4c60",
  "created": "2026-09-15T10:12:33Z",
  "recipients": [
    "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p"
  ],
  "allowed_signers": [
    "secretgit namespaces=\"secretgit-v1\" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI... vault-3f9c2a1b7e5d4c60 device mac-mini"
  ]
}
```

- `vault_id`: 16 hex chars from a CSPRNG, chosen at `init`. Used as the key
  store account prefix and in signature identities; carries no meaning.
- `recipients`: age public keys. Order is irrelevant.
- `allowed_signers`: lines in the exact format `ssh-keygen -Y verify -f`
  expects. The principal is always the literal `secretgit`. The namespace
  restriction `namespaces="secretgit-v1"` prevents a signature made for any
  other purpose with the same key from being accepted here.

`vault.json.sig` is produced by
`ssh-keygen -Y sign -f <signing key> -n secretgit-v1 vault.json`.
Any change to `vault.json` (adding a recipient or signer) is a commit that
replaces both files, signed by a key that was *already* listed — the roster
can only be extended by an existing member, and history of the roster is the
vault repo's own git history.

Note that the very first `vault.json` is self-signed by the initial key. The
trust root for a fresh clone on a fresh machine is therefore the **signing
public key fingerprint printed on the recovery kit**, not the file itself.
Verify the fingerprint first, then the file.

## 4. Manifest

Plaintext JSON, encrypted with age to all recipients → `NNNNNN.manifest.age`,
then signed → `NNNNNN.manifest.age.sig` (signature over the *ciphertext*,
namespace `secretgit-v1`).

```json
{
  "format": "secretgit-manifest/1",
  "vault_id": "3f9c2a1b7e5d4c60",
  "repo_id": "9a1e4d0c5b2f7e83",
  "generation": 2,
  "kind": "incremental",
  "base": 1,
  "created": "2026-09-15T18:40:07Z",
  "prev_manifest_sha256": "c0ffee…",
  "source": {
    "label": "tradebot",
    "head": "refs/heads/main"
  },
  "refs": {
    "refs/heads/main": "8d3f…",
    "refs/heads/feature/x": "11aa…",
    "refs/tags/v1.2": "77bc…"
  },
  "prerequisites": ["4e2d…", "0b9f…"],
  "files": [
    {
      "name": "000002.bundle.age",
      "role": "bundle",
      "content_encoding": "none",
      "plaintext_sha256": "…",
      "ciphertext_sha256": "…",
      "size": 1834211
    },
    {
      "name": "000002.state.tar.age",
      "role": "state",
      "content_encoding": "zstd",
      "plaintext_sha256": "…",
      "ciphertext_sha256": "…",
      "size": 90112
    }
  ],
  "tool": "secretgit/0.1.0"
}
```

Field semantics:

- `kind`: `full` or `incremental`. A full generation has no `base` and
  empty `prerequisites`.
- `base`: for an incremental, the generation number its bundle builds on
  (always the *previous* generation in v1; the bundle's prerequisites are the
  ref tips recorded in that generation's manifest).
- `prev_manifest_sha256`: SHA-256 of the previous generation's
  **manifest ciphertext** (`NNNNNN.manifest.age`), or `null` for `000001`.
  This is the hash chain: a remote that silently drops or replaces a
  generation breaks the chain and `verify` reports it. It links across full
  generations too, so the chain spans the whole history of the repo in the
  vault.
- `refs`: the complete ref map of the source at backup time — every ref
  under `refs/heads/`, `refs/tags/`, and `refs/remotes/` if configured. This,
  not the bundle header, is the authority on what a restore must produce.
  Deleted branches are therefore restored as deleted.
- `source.head`: the symbolic target of `HEAD` (a ref name), or a sha when
  detached, so the restored working repo checks out the right thing.
- `prerequisites`: the commit ids the bundle assumes the receiver has
  (output of `git bundle list-heads` prerequisite lines). Restore verifies
  that all of them are present before fetching the bundle.
- `files`: every ciphertext this generation consists of, with plaintext and
  ciphertext hashes and ciphertext size. Verification order on restore:
  ciphertext hash → age decrypt (AEAD) → plaintext hash.
- `source.label` is a human hint only and may be omitted for stricter
  metadata hygiene; the tool's local config maps `repo_id` to a path.

## 5. Bundles

- Full: `git bundle create <out> --all`. `--all` includes `HEAD`; the tool
  additionally records the ref map in the manifest.
- Incremental: `git bundle create <out> --all ^<tip1> ^<tip2> …` where the
  tips are every sha in the base generation's `refs`. If the resulting object
  set would be empty (no ref changed), **no generation is written** and the
  run reports "nothing to back up"; state-only changes still produce a
  generation with a state archive and no bundle.
- A new **full** generation is written when any of these hold:
  - the number of incrementals since the last full ≥ `full_every` (default 20);
  - the sum of incremental ciphertext sizes since the last full exceeds the
    last full's size (default ratio 1.0);
  - the user passes `--full`;
  - the base generation's prerequisites are no longer reachable in the source
    (e.g. after an aggressive `git gc` following a history rewrite).
- Bundles are produced by the source's own `git` and consumed by the
  restoring machine's `git`; secretgit never parses pack data.

## 6. State archive

An optional POSIX tar. Compression before encryption is optional; the
default is `tar -cf -` piped to `zstd` when available, then age. The
manifest's `files[].role` is `state` and `content_encoding` is `zstd` or
`none`. Git bundles are already packed, so they are never compressed
(`content_encoding` is always `none` for role `bundle`). Paths inside are relative to the source repo root. What goes in is
configured per repo (`state.include`, `state.exclude`, `state.pre_hook`).
The pre-hook exists for things like SQLite `.backup` snapshots; it runs with
a staging directory path in `$SECRETGIT_STAGE` and whatever it writes there
is archived in addition to `state.include`.

## 7. Encryption details

- `age` in binary (not ASCII-armored) format, recipients = all keys in
  `vault.json` at the time of writing.
- Manifests are encrypted the same way as bundles; there is no plaintext
  metadata beyond `vault.json` and file names.
- File names carry generation number and role only.

## 8. Signatures

`ssh-keygen -Y sign -f <signing key> -n secretgit-v1 <file>` produces
`<file>.sig` in OpenSSH's `SSHSIG` armored format. Only manifests and
`vault.json` are signed; bundle and state ciphertexts are authenticated
through the hashes inside the signed manifest.

Verification of a generation, in order:

1. `vault.json.sig` verifies against a signer whose fingerprint you trust.
2. `NNNNNN.manifest.age.sig` verifies against `allowed_signers` from
   `vault.json`, namespace `secretgit-v1`.
3. `prev_manifest_sha256` equals the SHA-256 of `(NNNNNN-1).manifest.age`.
4. Decrypt the manifest (AEAD failure = tampering or wrong key).
5. Every `files[]` entry: ciphertext SHA-256 matches, decrypts, plaintext
   SHA-256 matches.

## 9. Restore semantics

Given a target generation `G`:

1. Find the nearest full generation `F ≤ G` in the chain.
2. `git clone --bare F.bundle` (or `git init` + `git fetch`).
3. For each incremental `F+1 … G` in order: check prerequisites are
   present, `git fetch <bundle> '+refs/heads/*:refs/heads/*' '+refs/tags/*:refs/tags/*'`.
4. Apply the manifest `refs` map of `G` exactly: create/update every listed
   ref, delete any ref not listed. Set `HEAD` from `source.head`.
5. `git fsck --connectivity-only` must pass.
6. Optionally unpack the state archive of `G` into the working tree.

The **restore proof** that ends every `backup` run is exactly this procedure
executed from a fresh fetch of the vault remote into a private temporary
directory, followed by a comparison of the rebuilt ref map with the source's
current ref map. `status` records the timestamp and generation of the last
successful proof; a backup without a green proof is reported as **failed**
even if the push succeeded.

## 10. Local state (not on the remote)

Kept in the source repo's `.git/secretgit/` (so it follows the repo and is
never committed):

- `config.json`: vault remote URL, `repo_id`, state include/exclude/pre_hook,
  `full_every`, schedule.
- `status.json`: last generation written, last proof time and generation,
  last error.
- A cache clone of the vault repo (shallow or partial) to avoid re-cloning.

Key material lives only in the OS key store under service `secretgit`,
accounts `<vault_id>/age-identity` and `<vault_id>/signing-key`, and on the
printed recovery kit.

## 11. Versioning

`format` strings carry a major version. A tool must refuse to write into a
vault whose `vault.json` format major it does not understand and must be
able to read every earlier major it ever wrote. Changes that keep
[restore-by-hand.md](restore-by-hand.md) valid are minor and need no bump.
