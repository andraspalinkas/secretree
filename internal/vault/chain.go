package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"secretgit/internal/crypt"
	"secretgit/internal/gitx"
	"secretgit/internal/keys"
)

// Reader reads files out of a git clone of the vault at HEAD. The clone may
// be a partial (--filter=blob:none --no-checkout) clone: blobs are fetched
// on demand, which is what makes the restore proof cheap on big vaults.
type Reader struct{ Dir string }

// ReadFile returns a small file's bytes.
func (r *Reader) ReadFile(rel string) ([]byte, error) {
	out, err := gitx.Run(r.Dir, "cat-file", "blob", "HEAD:"+rel)
	if err != nil {
		return nil, fmt.Errorf("vault: read %s: %w", rel, err)
	}
	return []byte(out), nil
}

// ExtractFile streams a file to disk.
func (r *Reader) ExtractFile(rel, dst string) error {
	if err := gitx.RunToFile(r.Dir, dst, "cat-file", "blob", "HEAD:"+rel); err != nil {
		return fmt.Errorf("vault: extract %s: %w", rel, err)
	}
	return nil
}

// Exists reports whether a path is in the vault tree.
func (r *Reader) Exists(rel string) bool {
	_, err := gitx.Run(r.Dir, "cat-file", "-e", "HEAD:"+rel)
	return err == nil
}

// Empty reports whether the vault has no commits yet.
func (r *Reader) Empty() bool {
	_, err := gitx.Run(r.Dir, "rev-parse", "--verify", "-q", "HEAD")
	return err != nil
}

// ListRepos lists repo ids present in the vault.
func (r *Reader) ListRepos() ([]string, error) {
	if !r.Exists(ReposDir) {
		return nil, nil
	}
	out, err := gitx.Run(r.Dir, "ls-tree", "--name-only", "HEAD:"+ReposDir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			ids = append(ids, l)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// Generations lists the generation numbers stored for a repo, ascending.
func (r *Reader) Generations(repoID string) ([]int, error) {
	if !r.Exists(RepoDir(repoID)) {
		return nil, nil
	}
	out, err := gitx.Run(r.Dir, "ls-tree", "--name-only", "HEAD:"+RepoDir(repoID))
	if err != nil {
		return nil, err
	}
	var gens []int
	for _, name := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasSuffix(name, ".manifest.age") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(name, ".manifest.age"))
		if err != nil {
			return nil, fmt.Errorf("vault: unexpected file %s", name)
		}
		gens = append(gens, n)
	}
	sort.Ints(gens)
	return gens, nil
}

// LoadMeta reads and verifies vault.json. trusted must be one of the listed
// signers: it is the fingerprint the recovery kit vouches for, and it is what
// turns a self-signed roster into something worth believing.
func LoadMeta(r *Reader, trusted ssh.PublicKey) (*Meta, []ssh.PublicKey, error) {
	data, err := r.ReadFile(MetaFile)
	if err != nil {
		return nil, nil, err
	}
	sig, err := r.ReadFile(MetaFile + ".sig")
	if err != nil {
		return nil, nil, err
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, nil, fmt.Errorf("vault.json: %w", err)
	}
	if !strings.HasPrefix(m.Format, "secretgit-vault/1") {
		return nil, nil, fmt.Errorf("vault.json: unsupported format %q", m.Format)
	}
	signers, err := keys.ParseAllowedSigners(m.AllowedSigners)
	if err != nil {
		return nil, nil, fmt.Errorf("vault.json: %w", err)
	}
	found := false
	for _, s := range signers {
		if string(s.Marshal()) == string(trusted.Marshal()) {
			found = true
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("vault.json: our signing key %s is not an allowed signer of this vault", ssh.FingerprintSHA256(trusted))
	}
	if _, err := crypt.Verify(data, sig, signers); err != nil {
		return nil, nil, fmt.Errorf("vault.json: %w", err)
	}
	return &m, signers, nil
}

// Generation is one verified, decrypted manifest.
type Generation struct {
	Num                int
	Manifest           *Manifest
	ManifestCipherHash string
}

// Chain is the verified sequence of generations of one repo.
type Chain struct {
	RepoID string
	Gens   []Generation
}

// Last returns the newest generation or nil.
func (c *Chain) Last() *Generation {
	if len(c.Gens) == 0 {
		return nil
	}
	return &c.Gens[len(c.Gens)-1]
}

// Get returns generation n or nil.
func (c *Chain) Get(n int) *Generation {
	for i := range c.Gens {
		if c.Gens[i].Num == n {
			return &c.Gens[i]
		}
	}
	return nil
}

// Bytes is the total ciphertext size of the chain.
func (c *Chain) Bytes() int64 {
	var n int64
	for _, g := range c.Gens {
		for _, f := range g.Manifest.Files {
			n += f.Size
		}
	}
	return n
}

// LoadChain reads every generation of a repo, verifying signature, hash
// chain and manifest contents. Any gap or mismatch is an error: the chain is
// either whole or it is not.
func LoadChain(r *Reader, vaultID, repoID string, identity age.Identity, signers []ssh.PublicKey) (*Chain, error) {
	nums, err := r.Generations(repoID)
	if err != nil {
		return nil, err
	}
	chain := &Chain{RepoID: repoID}
	var prevHash *string
	for i, n := range nums {
		if n != i+1 {
			return nil, fmt.Errorf("chain %s: generation %06d missing (found %06d)", repoID, i+1, n)
		}
		cipher, err := r.ReadFile(ManifestPath(repoID, n))
		if err != nil {
			return nil, err
		}
		sig, err := r.ReadFile(SigPath(repoID, n))
		if err != nil {
			return nil, err
		}
		if _, err := crypt.Verify(cipher, sig, signers); err != nil {
			return nil, fmt.Errorf("generation %06d: %w", n, err)
		}
		plain, err := crypt.DecryptBytes(cipher, identity)
		if err != nil {
			return nil, fmt.Errorf("generation %06d manifest: %w", n, err)
		}
		var m Manifest
		if err := json.Unmarshal(plain, &m); err != nil {
			return nil, fmt.Errorf("generation %06d manifest: %w", n, err)
		}
		if err := checkManifest(&m, vaultID, repoID, n, prevHash); err != nil {
			return nil, err
		}
		h := crypt.SHA256Bytes(cipher)
		chain.Gens = append(chain.Gens, Generation{Num: n, Manifest: &m, ManifestCipherHash: h})
		prevHash = &h
	}
	return chain, nil
}

func checkManifest(m *Manifest, vaultID, repoID string, n int, prevHash *string) error {
	if !strings.HasPrefix(m.Format, "secretgit-manifest/1") {
		return fmt.Errorf("generation %06d: unsupported manifest format %q", n, m.Format)
	}
	if m.VaultID != vaultID || m.RepoID != repoID || m.Generation != n {
		return fmt.Errorf("generation %06d: manifest identity mismatch (vault %s repo %s gen %d)", n, m.VaultID, m.RepoID, m.Generation)
	}
	switch m.Kind {
	case KindFull:
		if m.Base != nil || len(m.Prerequisites) != 0 || m.File(RoleBundle) == nil {
			return fmt.Errorf("generation %06d: malformed full manifest", n)
		}
	case KindIncremental:
		if m.Base == nil || *m.Base != n-1 {
			return fmt.Errorf("generation %06d: incremental must base on %06d", n, n-1)
		}
	default:
		return fmt.Errorf("generation %06d: unknown kind %q", n, m.Kind)
	}
	switch {
	case prevHash == nil && m.PrevManifestSHA256 != nil:
		return fmt.Errorf("generation %06d: first manifest must not link to a predecessor", n)
	case prevHash != nil && (m.PrevManifestSHA256 == nil || *m.PrevManifestSHA256 != *prevHash):
		return fmt.Errorf("generation %06d: hash chain broken (a generation was altered or replaced)", n)
	}
	for _, f := range m.Files {
		if !strings.HasPrefix(f.Name, GenName(n)+".") || strings.Contains(f.Name, "/") {
			return fmt.Errorf("generation %06d: file %q does not belong to it", n, f.Name)
		}
	}
	return nil
}

// RestorePlan lists the generations needed to rebuild target: the nearest
// full at or before it, then every generation up to target.
func (c *Chain) RestorePlan(target int) ([]Generation, error) {
	if target <= 0 && c.Last() != nil {
		target = c.Last().Num
	}
	if c.Get(target) == nil {
		return nil, fmt.Errorf("generation %06d does not exist", target)
	}
	start := -1
	for i := range c.Gens {
		if c.Gens[i].Num <= target && c.Gens[i].Manifest.Kind == KindFull {
			start = i
		}
	}
	if start < 0 {
		return nil, errors.New("no full generation before target")
	}
	var plan []Generation
	for _, g := range c.Gens[start:] {
		if g.Num > target {
			break
		}
		plan = append(plan, g)
	}
	return plan, nil
}
