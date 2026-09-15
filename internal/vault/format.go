// Package vault defines the on-remote format (see docs/vault-format.md) and
// verifies chains of generations.
package vault

import (
	"fmt"
	"path"
	"time"
)

const (
	FormatVault    = "secretgit-vault/1"
	FormatManifest = "secretgit-manifest/1"

	KindFull        = "full"
	KindIncremental = "incremental"

	RoleBundle = "bundle"
	RoleState  = "state"

	EncodingNone = "none"
	EncodingZstd = "zstd"

	MetaFile   = "vault.json"
	ReadmeFile = "README.md"
	ReposDir   = "repos"
)

// Meta is vault.json: the only plaintext metadata on the remote.
type Meta struct {
	Format         string    `json:"format"`
	VaultID        string    `json:"vault_id"`
	Created        time.Time `json:"created"`
	Recipients     []string  `json:"recipients"`
	AllowedSigners []string  `json:"allowed_signers"`
	Members        []Member  `json:"members,omitempty"`
	// RevokedSigners keeps former signers so that what they signed while
	// they were members still verifies; nothing after LastGeneration /
	// LastLedger may carry their signature.
	RevokedSigners []RevokedSigner `json:"revoked_signers,omitempty"`
}

// RevokedSigner is a former allowed_signers line with its cut-off points.
type RevokedSigner struct {
	Line           string    `json:"line"`
	Fingerprint    string    `json:"fingerprint"`
	RevokedAt      time.Time `json:"revoked_at"`
	LastGeneration int       `json:"last_generation"`
	LastLedger     int       `json:"last_ledger"`
}

// Member pairs a recipient with a name and its device's signer, for
// display and removal. Optional; the recipients and allowed_signers lists
// remain authoritative.
type Member struct {
	Name              string    `json:"name,omitempty"`
	Recipient         string    `json:"recipient"`
	SignerFingerprint string    `json:"signer_fingerprint,omitempty"`
	Added             time.Time `json:"added"`
}

// Manifest describes one generation. It is encrypted, then signed.
type Manifest struct {
	Format             string            `json:"format"`
	VaultID            string            `json:"vault_id"`
	RepoID             string            `json:"repo_id"`
	Generation         int               `json:"generation"`
	Kind               string            `json:"kind"`
	Base               *int              `json:"base,omitempty"`
	Created            time.Time         `json:"created"`
	PrevManifestSHA256 *string           `json:"prev_manifest_sha256"`
	Source             Source            `json:"source"`
	Refs               map[string]string `json:"refs"`
	Prerequisites      []string          `json:"prerequisites"`
	Files              []FileEntry       `json:"files"`
	Tool               string            `json:"tool"`
}

// Source records what HEAD pointed at and a human label.
type Source struct {
	Label string `json:"label,omitempty"`
	Head  string `json:"head"`
}

// FileEntry is one ciphertext belonging to a generation.
type FileEntry struct {
	Name             string `json:"name"`
	Role             string `json:"role"`
	ContentEncoding  string `json:"content_encoding"`
	PlaintextSHA256  string `json:"plaintext_sha256"`
	CiphertextSHA256 string `json:"ciphertext_sha256"`
	Size             int64  `json:"size"`
}

// File returns the entry with the given role, or nil.
func (m *Manifest) File(role string) *FileEntry {
	for i := range m.Files {
		if m.Files[i].Role == role {
			return &m.Files[i]
		}
	}
	return nil
}

// GenName formats a generation number.
func GenName(n int) string { return fmt.Sprintf("%06d", n) }

// RepoDir is the vault-relative directory of a source repo.
func RepoDir(repoID string) string { return path.Join(ReposDir, repoID) }

// ManifestPath, SigPath, BundlePath and StatePath are vault-relative names.
func ManifestPath(repoID string, n int) string {
	return path.Join(RepoDir(repoID), GenName(n)+".manifest.age")
}
func SigPath(repoID string, n int) string { return ManifestPath(repoID, n) + ".sig" }
func BundleName(n int) string             { return GenName(n) + ".bundle.age" }
func StateName(n int) string              { return GenName(n) + ".state.tar.age" }
