# Security policy

secretgit exists to protect source code from its own hosting provider.
Please report vulnerabilities privately rather than in a public issue.

**Report to:** open a GitHub security advisory on this repository
(Security → Advisories → Report a vulnerability), or e-mail the maintainer
listed in the repository profile. Expect an acknowledgement within a few
days.

**In scope:** anything that lets a party without a key learn plaintext
(source, refs, messages, PR data, CI logs) from the vault; anything that
lets a non-signer append a generation, ledger entry or collaboration event
that other members accept; silent data loss in backup, sync or restore.

**Out of scope by design** (see `docs/threat-model.md`): compromise of a
machine that holds a key; traffic analysis of ciphertext sizes and timing;
availability of the host.

**Cryptography:** secretgit contains no cryptographic primitives of its
own. Encryption is age (filippo.io/age), signatures are OpenSSH signatures
(github.com/hiddeco/sshsig over golang.org/x/crypto/ssh). Reports about
those libraries belong upstream, but tell us too so we can pin a fixed
version.
