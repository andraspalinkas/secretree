# Restore by hand (no secretree needed)

Use this when the machine that ran secretree is gone. You need:

- the **recovery kit** (printed at `init`): the age identity
  (`AGE-SECRET-KEY-1…`), the signing public key fingerprint, and the vault
  remote URL;
- `git` (any version from the last decade), `age` (or `rage`), `ssh-keygen`
  (OpenSSH ≥ 8.2 for `-Y`), `shasum` or `sha256sum`, and a shell;
- read access to the vault remote.

A copy of this file is also stored as `README.md` inside every vault.

## 0. Get the vault

```bash
git clone <vault-url> vault && cd vault
```

Note the repo id you want under `repos/`; if there are several and you did
not record which is which, the `source.label` inside each manifest tells you
once decrypted.

## 1. Establish trust: verify `vault.json`

Extract the signing public key(s) from `vault.json` into an
`allowed_signers` file and compare the fingerprint to the recovery kit
**before** trusting the file.

```bash
python3 - <<'PY' > allowed_signers
import json; [print(l) for l in json.load(open("vault.json"))["allowed_signers"]]
PY
# fingerprint(s) — must match the recovery kit
awk '{print $3, $4}' allowed_signers | ssh-keygen -lf -
ssh-keygen -Y verify -f allowed_signers -I secretree -n secretree-v1 \
  -s vault.json.sig < vault.json
```

`Good "secretree" signature …` is the only acceptable output.

## 2. Put the age identity somewhere private

```bash
umask 077
mkdir -p /tmp/sg && printf '%s\n' 'AGE-SECRET-KEY-1…' > /tmp/sg/key.txt
```

(On a machine you will keep, prefer a RAM disk or the OS key store; delete
the file when done.)

## 3. Verify and decrypt the chain

Pick the target generation `G` (usually the last one). Walk from the
nearest **full** generation up to `G`; for each generation `N`:

```bash
R=repos/<repo-id>
N=000001   # repeat for each generation up to G

# a) signature over the manifest ciphertext
ssh-keygen -Y verify -f allowed_signers -I secretree -n secretree-v1 \
  -s $R/$N.manifest.age.sig < $R/$N.manifest.age

# b) decrypt the manifest
age -d -i /tmp/sg/key.txt $R/$N.manifest.age > /tmp/sg/$N.manifest.json
cat /tmp/sg/$N.manifest.json          # read kind/base/prev_manifest_sha256/files

# c) chain link (skip for 000001): must equal prev_manifest_sha256 in this manifest
shasum -a 256 $R/<previous N>.manifest.age

# d) every file listed in "files": ciphertext hash, then decrypt, then plaintext hash
shasum -a 256 $R/$N.bundle.age        # compare with files[].ciphertext_sha256
age -d -i /tmp/sg/key.txt $R/$N.bundle.age > /tmp/sg/$N.bundle
shasum -a 256 /tmp/sg/$N.bundle       # compare with files[].plaintext_sha256
# same for $N.state.tar.age if present
```

Which generations you need: find the largest `N ≤ G` whose manifest has
`"kind": "full"`; that is the start. Every generation between it and `G`
must be applied in order. Earlier generations are not needed for a restore
(they are needed only to verify the full chain).

## 4. Rebuild the repository

```bash
git clone --bare /tmp/sg/<full N>.bundle restored.git
cd restored.git
# for each later generation in order that has a bundle (some have none):
git bundle verify /tmp/sg/<N>.bundle              # checks prerequisites are present
git fetch --no-tags /tmp/sg/<N>.bundle '+refs/*:refs/*'
```

Then make the refs match the manifest of `G` exactly (this also removes
branches that were deleted in the source before the backup):

```bash
python3 - <<'PY' | git update-ref --stdin
import json
m = json.load(open("/tmp/sg/<G>.manifest.json"))
for ref, sha in m["refs"].items():
    print(f"update {ref} {sha}")
PY
git for-each-ref --format='%(refname)' | grep -vxF -f <(python3 -c '
import json; [print(r) for r in json.load(open("/tmp/sg/<G>.manifest.json"))["refs"]]') \
  | sed 's/^/delete /' | git update-ref --stdin
git symbolic-ref HEAD "$(python3 -c 'import json;print(json.load(open("/tmp/sg/<G>.manifest.json"))["source"]["head"])')"
git fsck --connectivity-only
```

Finally get a working copy and, if there was a state archive, unpack it:

```bash
cd .. && git clone restored.git restored && cd restored
tar -xf /tmp/sg/<G>.state.tar          # or: zstd -dc … | tar -x   if content_encoding is zstd
```

## 5. Clean up

```bash
rm -rf /tmp/sg
```

Then run secretree `init --from-recovery-kit` on the new machine to put the
identity back into the key store and resume backups into the **same** vault.
