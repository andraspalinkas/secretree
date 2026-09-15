// Package config holds per-repository local state under .git/secretgit/.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Dir is the name of the state directory inside the git dir.
const Dir = "secretgit"

// Config is .git/secretgit/config.json.
type Config struct {
	VaultURL    string  `json:"vault_url"`
	VaultBranch string  `json:"vault_branch"`
	VaultID     string  `json:"vault_id"`
	RepoID      string  `json:"repo_id"`
	Label       string  `json:"label,omitempty"`
	FullEvery   int     `json:"full_every"`
	FullRatio   float64 `json:"full_ratio"`
	State       State   `json:"state"`
}

// State configures the out-of-repo state archive.
type State struct {
	Include []string `json:"include,omitempty"`  // paths relative to the work tree
	Exclude []string `json:"exclude,omitempty"`  // glob patterns matched against relative paths
	PreHook string   `json:"pre_hook,omitempty"` // shell command; $SECRETGIT_STAGE is a staging dir
}

// Status is .git/secretgit/status.json.
type Status struct {
	LastGeneration      int        `json:"last_generation"`
	LastBackup          *time.Time `json:"last_backup,omitempty"`
	LastProof           *time.Time `json:"last_proof,omitempty"`
	LastProofGeneration int        `json:"last_proof_generation"`
	LastError           string     `json:"last_error,omitempty"`
	LastErrorTime       *time.Time `json:"last_error_time,omitempty"`
	ChainBytes          int64      `json:"chain_bytes"`
	Generations         int        `json:"generations"`
}

// Paths resolves the state directory for a git dir.
type Paths struct {
	Root   string // <gitdir>/secretgit
	Config string
	Status string
	Cache  string // vault working clone
	Tmp    string
}

// NewPaths builds the path set.
func NewPaths(gitDir string) Paths {
	root := filepath.Join(gitDir, Dir)
	return Paths{
		Root:   root,
		Config: filepath.Join(root, "config.json"),
		Status: filepath.Join(root, "status.json"),
		Cache:  filepath.Join(root, "vault-cache"),
		Tmp:    filepath.Join(root, "tmp"),
	}
}

// ErrNotInitialised means `secretgit init` has not run here.
var ErrNotInitialised = errors.New("this repository is not initialised for secretgit (run: secretgit init --vault <url>)")

// Load reads config.json.
func Load(p Paths) (*Config, error) {
	data, err := os.ReadFile(p.Config)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInitialised
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config.json: %w", err)
	}
	if c.FullEvery <= 0 {
		c.FullEvery = 20
	}
	if c.FullRatio <= 0 {
		c.FullRatio = 1.0
	}
	return &c, nil
}

// Save writes config.json.
func Save(p Paths, c *Config) error {
	if err := os.MkdirAll(p.Root, 0o700); err != nil {
		return err
	}
	return writeJSON(p.Config, c)
}

// LoadStatus reads status.json; a missing file is an empty status.
func LoadStatus(p Paths) (*Status, error) {
	data, err := os.ReadFile(p.Status)
	if errors.Is(err, os.ErrNotExist) {
		return &Status{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("status.json: %w", err)
	}
	return &s, nil
}

// SaveStatus writes status.json.
func SaveStatus(p Paths, s *Status) error {
	if err := os.MkdirAll(p.Root, 0o700); err != nil {
		return err
	}
	return writeJSON(p.Status, s)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
