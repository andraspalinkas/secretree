package vault

// Readme is stored in plaintext at the root of every vault so that a
// restore is possible from the vault alone plus the recovery kit.
const Readme = `# secretree vault

This repository is **blind storage**. Everything under ` + "`repos/`" + ` is ciphertext
(age, X25519 + ChaCha20-Poly1305) and every manifest is signed (OpenSSH
signature, namespace ` + "`secretree-v1`" + `). Without the recovery kit nothing here
can be read, not by the host, not by anyone.

Layout: ` + "`repos/<repo-id>/NNNNNN.{manifest.age,manifest.age.sig,bundle.age,state.tar.age}`" + `.
Each NNNNNN is one backup generation; a chain is a ` + "`full`" + ` generation followed
by incrementals. ` + "`vault.json`" + ` lists recipients and allowed signers.

## Restore with secretree

    secretree init --from-recovery-kit kit.txt --vault <this repo's URL>
    secretree restore --repo-id <id> --to ./restored

## Restore by hand (git + age + ssh-keygen + shasum only)

1. Verify vault.json: extract its "allowed_signers" lines to a file, check the
   key fingerprint against the recovery kit, then
   ` + "`ssh-keygen -Y verify -f allowed_signers -I secretree -n secretree-v1 -s vault.json.sig < vault.json`" + `.
2. Put the age identity from the kit into a private file key.txt.
3. For the target generation G, find the nearest earlier "full" manifest F:
   for each N from F to G:
   ` + "`ssh-keygen -Y verify ... -s N.manifest.age.sig < N.manifest.age`" + `,
   ` + "`age -d -i key.txt N.manifest.age > N.json`" + `, check
   prev_manifest_sha256 == sha256 of the previous manifest.age, then for each
   entry in "files": compare ciphertext sha256, ` + "`age -d`" + `, compare plaintext sha256.
4. ` + "`git clone --bare F.bundle restored.git`" + `; for each later bundle in order:
   ` + "`git bundle verify`" + ` then ` + "`git fetch <bundle> '+refs/*:refs/*'`" + `.
5. Make refs equal to the "refs" map of G (` + "`git update-ref --stdin`" + `), set HEAD
   to "source.head", run ` + "`git fsck --connectivity-only`" + `.
6. ` + "`git clone restored.git restored`" + ` and unpack G's state.tar (zstd if the
   manifest says content_encoding is zstd) into it.

Full procedure with copy-pasteable commands: docs/restore-by-hand.md in the
secretree source repository.
`
